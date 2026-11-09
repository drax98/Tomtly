package extension

import (
	"context"
	"crypto/rand"

	"crypto/subtle"
	"encoding/base64"

	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"home-control/internal/store"
)

const (
	// MaxWebhookBody is the biggest message an extension is handed.
	MaxWebhookBody = 1 << 20
	webhookRate    = 300 // messages a minute for one extension, with the right token
	badHookRate    = 60  // wrong tokens a minute from one address
	maxHookWindows = 10000
)

// webhookTimeout is a variable so a test can wait less.
var webhookTimeout = 10 * time.Second

// The errors a caller can be given. The first is the same for a wrong token, an unknown extension and one that doesn't
// take webhooks, so none of them can be told from the outside.
var (
	ErrNoWebhook      = errors.New("not found")
	ErrWebhookLimit   = errors.New("too many messages")
	ErrWebhookBusy    = errors.New("the extension is busy")
	ErrWebhookTimeout = errors.New("the extension took too long")
	errWebhookStore   = errors.New("webhooks are unavailable right now")
)

// WebhookRequest is what an extension's webhooks.receive is given.
type WebhookRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	BodyHex string            `json:"bodyHex"`
}

// WebhookResponse is what goes back to the caller.
type WebhookResponse struct {
	Status      int
	ContentType string
	Headers     map[string]string
	Body        string
}

// Deliver hands a message to an extension that has already been checked, and waits for its answer.
type Deliver func(ctx context.Context, req WebhookRequest) (WebhookResponse, error)

type pendingHook struct {
	req      WebhookRequest
	deadline time.Time
	done     chan hookResult
}

type hookResult struct {
	resp WebhookResponse
	err  error
}

func hashToken(token string) string { return sha256Hex([]byte(token)) }

// newWebhookToken replaces an extension's token. Only the hash is kept, so the path returned is the one time it can be shown.
func (m *Manager) newWebhookToken(ctx context.Context, id string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := m.store.SetWebhookTokenHash(ctx, id, hashToken(token)); err != nil {
		return "", err
	}
	m.hookHashes.Store(id, hashToken(token))
	return "/hooks/" + id + "/" + token, nil
}

// firstWebhook makes the token when webhooks are granted and there is none yet, and returns "" when one already exists.
func (m *Manager) firstWebhook(ctx context.Context, id string, keys []string) (string, error) {
	if !slices.Contains(keys, "webhooks") {
		return "", nil
	}
	_, err := m.store.WebhookTokenHash(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return m.newWebhookToken(ctx, id)
	}
	return "", err
}

// NewWebhookToken gives an extension a new webhook address and returns it once. The old one stops working at once.
func (m *Manager) NewWebhookToken(ctx context.Context, source string) (string, error) {
	inst := m.bySource(source)
	if inst == nil {
		return "", errors.New("no extension from that source")
	}
	id, _ := inst.identity()
	if !inst.takesWebhooks() {
		return "", errors.New("this extension doesn't receive webhooks")
	}
	if !inst.granted(ctx) {
		return "", errors.New("approve this extension first")
	}
	saving, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	return m.newWebhookToken(saving, id)
}

func (m *Manager) bySource(source string) *instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.instances {
		if inst.entry.Source == source {
			return inst
		}
	}
	return nil
}

func (i *instance) takesWebhooks() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.hooked
}

// hookLimit counts messages per key per minute: a real sender's per extension, and wrong tokens' per address.
type hookLimit struct {
	mu      sync.Mutex
	windows map[string]hookWindow
}

type hookWindow struct {
	start time.Time
	count int
}

func (h *hookLimit) allow(key string, now time.Time, limit int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.windows == nil {
		h.windows = map[string]hookWindow{}
	}
	if len(h.windows) >= maxHookWindows {
		for k, w := range h.windows {
			if now.Sub(w.start) >= time.Minute {
				delete(h.windows, k)
			}
		}
		if len(h.windows) >= maxHookWindows {
			key = "" // too many addresses at once share one allowance, so they can't grow this
		}
	}
	w := h.windows[key]
	if now.Sub(w.start) >= time.Minute {
		w = hookWindow{start: now}
	}
	w.count++
	h.windows[key] = w
	return w.count <= limit
}

var noToken = strings.Repeat("0", 64)

// tokenHash is an extension's token hash, or noToken if it has none, read from the store only the first time.
func (m *Manager) tokenHash(id string) (string, error) {
	if hash, ok := m.hookHashes.Load(id); ok {
		return hash.(string), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	hash, err := m.store.WebhookTokenHash(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		hash = noToken
	} else if err != nil {
		return "", errWebhookStore
	}
	m.hookHashes.Store(id, hash)
	return hash, nil
}

// Webhook checks the address a message came to and returns what delivers it. The token comes first, so a stranger's wrong
// tokens can't use up an extension's allowance or reach the store: they count only against their own address, and a right
// token never looks at that count, so the real sender gets through even from an address shared with the stranger.
func (m *Manager) Webhook(id, token, client string) (Deliver, error) {
	var target *instance
	m.mu.RLock()
	for _, inst := range m.instances {
		if inst.acceptsWebhooks(id) {
			target = inst
		}
	}
	m.mu.RUnlock()
	stored := noToken
	if target != nil {
		var err error
		if stored, err = m.tokenHash(id); err != nil {
			return nil, err
		}
	}
	// The comparison happens even when there is nothing to compare with, so it takes the same time.
	same := subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(stored)) == 1
	if target == nil || !same {
		if !m.badHooks.allow(client, time.Now(), badHookRate) {
			return nil, ErrWebhookLimit
		}
		return nil, ErrNoWebhook
	}
	if !m.hookRate.allow(id, time.Now(), webhookRate) {
		return nil, ErrWebhookLimit
	}
	return target.deliver, nil
}

// acceptsWebhooks says whether this is a running extension with that id that takes webhooks.
func (i *instance) acceptsWebhooks(id string) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.hooked && i.status.ID == id && i.status.Running && !i.quarantined
}

func (i *instance) deliver(ctx context.Context, req WebhookRequest) (WebhookResponse, error) {
	job := pendingHook{req: req, deadline: time.Now().Add(webhookTimeout), done: make(chan hookResult, 1)}
	select {
	case i.hooks <- job:
	default:
		return WebhookResponse{}, ErrWebhookBusy
	}
	timer := time.NewTimer(webhookTimeout)
	defer timer.Stop()
	select {
	case r := <-job.done:
		return r.resp, r.err
	case <-timer.C:
		return WebhookResponse{}, ErrWebhookTimeout
	case <-ctx.Done():
		return WebhookResponse{}, ctx.Err()
	}
}

// receive runs the extension's webhooks.receive on its own thread, like everything else it does.
func (i *instance) receive(l *loaded, api goja.Value, job pendingHook) {
	if !time.Now().Before(job.deadline) {
		job.done <- hookResult{err: ErrWebhookTimeout} // it waited behind a slow call, and the caller has gone
		return
	}
	i.note("webhook", "from outside")
	data, err := json.Marshal(job.req)
	if err != nil {
		job.done <- hookResult{err: err}
		return
	}
	value, err := i.callWithin(time.Until(job.deadline), l, l.receive, api, parseJSON(l.vm, string(data)))
	var resp WebhookResponse
	if err == nil {
		resp, err = i.webhookResponse(l, value)
	}
	if err != nil {
		err = errors.New(i.scrub(plainError(err)))
		i.setError(err)
	}
	job.done <- hookResult{resp, err}
}

var (
	headerName  = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	contentType = regexp.MustCompile(`^[A-Za-z0-9.+/;=\- ]{1,100}$`)
)

// webhookResponse turns what receive returned into a response. A secret in it is replaced rather than sent. Only a few
// headers pass, and never a cookie, so a message to this address can't set one in a browser.
func (i *instance) webhookResponse(l *loaded, value goja.Value) (WebhookResponse, error) {
	text, err := stringify(l.vm, value)
	if err != nil {
		return WebhookResponse{}, err
	}
	if text == "" || text == "null" {
		return WebhookResponse{Status: 204}, nil
	}
	if len(text) > maxOutput {
		return WebhookResponse{}, fmt.Errorf("returned over %d bytes", maxOutput)
	}
	// Unlike one secret, a parsed file has no string to replace: its pieces only show together.
	if i.holdsFile(text) {
		return WebhookResponse{}, errors.New("returned one of its file settings")
	}
	var out struct {
		Status  int               `json:"status"`
		Body    json.RawMessage   `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return WebhookResponse{}, errors.New("webhooks.receive must return nothing, or { status, body, headers }")
	}
	resp := WebhookResponse{Status: out.Status, ContentType: "text/plain; charset=utf-8", Headers: map[string]string{}}
	if resp.Status == 0 {
		resp.Status = 200
	}
	if resp.Status < 200 || resp.Status > 599 {
		return WebhookResponse{}, fmt.Errorf("status must be 200 to 599, not %d", resp.Status)
	}
	if len(out.Body) > 0 {
		var asText string
		if json.Unmarshal(out.Body, &asText) == nil {
			resp.Body = i.scrub(asText)
		} else {
			var tree any
			if err := json.Unmarshal(out.Body, &tree); err != nil {
				return WebhookResponse{}, err
			}
			clean, err := json.Marshal(i.scrubTree(tree))
			if err != nil {
				return WebhookResponse{}, err
			}
			resp.Body, resp.ContentType = string(clean), "application/json"
		}
	}
	for name, v := range out.Headers {
		v = i.scrub(v)
		lower := strings.ToLower(name)
		switch {
		case !headerName.MatchString(name) || strings.ContainsAny(v, "\r\n"):
		case lower == "content-type":
			if contentType.MatchString(v) {
				resp.ContentType = v
			}
		case lower == "cache-control" || lower == "etag" || lower == "retry-after" || strings.HasPrefix(lower, "x-"):
			resp.Headers[name] = v
		}
	}
	return resp, nil
}

// scrubTree removes secrets from every string in decoded JSON, where the text form would hide them behind escapes.
func (i *instance) scrubTree(v any) any {
	switch v := v.(type) {
	case string:
		return i.scrub(v)
	case []any:
		for n := range v {
			v[n] = i.scrubTree(v[n])
		}
	case map[string]any:
		clean := make(map[string]any, len(v))
		for k, item := range v {
			clean[i.scrub(k)] = i.scrubTree(item)
		}
		return clean
	}
	return v
}
