package extension

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"

	"tomtly/internal/store"
)

const (
	maxViolations     = 10 // since the last approval
	maxOpsPerMinute   = 600
	violationDetail   = 300
	violationStoreMax = time.Second
	violationsShown   = 20
)

// violate records something the extension tried and wasn't allowed, and returns the error to throw into it. It counts
// here, in Go, so an extension that catches the error can't hide the attempt.
func (i *instance) violate(kind, detail string) error {
	i.mu.RLock()
	stopped := i.quarantined
	i.mu.RUnlock()
	if stopped {
		return errors.New("this extension was stopped for breaking its access") // already counted; don't pile on
	}
	detail = i.scrub(detail)
	if len(detail) > violationDetail {
		detail = detail[:violationDetail] + "..."
	}
	id, _ := i.identity()
	recording, cancel := context.WithTimeout(context.Background(), violationStoreMax)
	defer cancel()
	if err := i.manager.store.RecordViolation(recording, store.Violation{Time: time.Now(), Extension: id, Kind: kind, Detail: detail}); err != nil {
		slog.Warn("extension violation could not be saved", "extension", id, "err", err)
	}
	slog.Warn("extension violation", "extension", id, "kind", kind, "detail", detail)
	i.mu.Lock()
	i.violations++
	stop := i.violations >= maxViolations && !i.quarantined
	i.mu.Unlock()
	if stop {
		i.quarantine(kind, detail)
	}
	return errors.New(detail)
}

// resumeViolations carries on the count since the last approval from what the store holds.
func (i *instance) resumeViolations(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	if recorded, err := i.manager.store.Violations(ctx, id, maxViolations); err == nil {
		i.mu.Lock()
		i.violations = max(i.violations, len(recorded))
		i.mu.Unlock()
	}
}

// quarantine stops an extension that keeps breaking its declared access, and takes its grants back so only the owner
// can start it again.
func (i *instance) quarantine(kind, detail string) {
	i.mu.Lock()
	i.quarantined = true
	i.status.Running, i.status.NeedsApproval = false, true
	if kind == "memory" {
		i.status.Error = "Stopped for using too much memory (" + detail + "). Approve it again to restart it."
	} else if kind == stoppedByOwner {
		i.status.Error = "You stopped this. Approve it again to restart it."
	} else {
		i.status.Error = fmt.Sprintf("Stopped after %d violations of its declared access (the last was %s: %s). Approve it again to restart it.", maxViolations, kind, detail)
	}
	for j := range i.devices {
		i.devices[j].Online = false
	}
	id, stopRun := i.status.ID, i.stopRun
	i.mu.Unlock()
	revoking, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := i.manager.store.SetGrants(revoking, id, nil); err != nil {
		slog.Warn("extension grants could not be revoked", "extension", id, "err", err)
	}
	if stopRun != nil {
		stopRun() // interrupts the VM and closes its connections
	}
}

// spend counts one request or socket operation against the extension's limit per minute.
func (i *instance) spend() error {
	now := time.Now()
	i.mu.Lock()
	if i.quarantined {
		i.mu.Unlock()
		return errors.New("this extension was stopped for breaking its access")
	}
	if now.Sub(i.windowStart) >= time.Minute {
		i.windowStart, i.ops = now, 0
	}
	i.ops++
	over := i.ops > maxOpsPerMinute
	i.mu.Unlock()
	if over {
		return i.violate("flood", fmt.Sprintf("over %d requests or socket operations in a minute", maxOpsPerMinute))
	}
	return nil
}

// binding is a secret setting and the only places it may go.
type binding struct {
	key        string
	forms      []string // the ways it can appear, except hex, which is compared in lower case
	hexForm    string
	restricted bool
	hosts      []string
	whole      bool            // it is a file's whole text, not one field of it
	chunks     map[string]bool // for a file, what recognises a stretch of it
}

func bindingsFor(l *loaded, settings map[string]any) []binding {
	var out []binding
	for _, key := range sorted(l.Settings) {
		s := l.Settings[key]
		value, _ := settings[key].(string)
		if s.Type == "file" && value != "" {
			// Nothing in a file may leave as it is: no host is listed, so every destination is refused.
			whole := fileTexts(value)
			for n, part := range append(whole, secretFieldValues(value, s.SecretFields)...) {
				b := binding{key: key, forms: secretForms(part), hexForm: hex.EncodeToString([]byte(part)), restricted: true, whole: n < len(whole)}
				if n == 0 {
					b.chunks = chunksOf(value)
				}
				out = append(out, b)
			}
			continue
		}
		if s.Type != "secret" || value == "" {
			continue
		}
		b := binding{key: key, forms: secretForms(value), hexForm: hex.EncodeToString([]byte(value)), restricted: true} // with no sendTo it goes nowhere: where a key may go is what the owner approves
		for _, entry := range s.SendTo {
			if m := hostSetting.FindStringSubmatch(entry); m != nil {
				if host, ok := hostIn(settings, m[1]); ok {
					b.hosts = append(b.hosts, host)
				}
			} else {
				b.hosts = append(b.hosts, strings.ToLower(entry))
			}
		}
		out = append(out, b)
	}
	return out
}

// secretForms is a secret as it is, URL-encoded, and base64 in each alignment it can take inside a longer text, and
// the same again of it as JSON writes it inside a string, once or twice, since that needn't be in text that is JSON.
// shortcut: a secret that was hashed, split, reversed or otherwise transformed can't be recognised. The sendTo limit
// stops an honest extension's mistake and the obvious ways to leak, not a determined one; the network list does that.
func secretForms(secret string) []string {
	var forms []string
	for _, written := range slices.Compact([]string{secret, jsonEscaped(secret), jsonEscaped(jsonEscaped(secret))}) {
		forms = append(forms, written, url.QueryEscape(written), url.PathEscape(written))
		for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			for offset, skip := range []int{0, 2, 3} {
				text := encoding.EncodeToString(append(make([]byte, offset), written...))[skip:]
				if (offset+len(written))%3 != 0 {
					text = text[:len(text)-1] // the last character mixes in whatever follows
				}
				if len(text) >= 4 {
					forms = append(forms, text)
				}
			}
		}
	}
	return slices.Compact(forms)
}

func (b binding) appearsIn(text string) bool {
	return strings.Contains(strings.ToLower(text), b.hexForm) || slices.ContainsFunc(b.forms, func(f string) bool { return strings.Contains(text, f) }) || b.holdsChunk(text)
}

// leak refuses a payload bound for host (with its port, if it has one) that holds a secret which may not go there.
// A sendTo entry without a port covers every port of that host. A broadcast has no host for sendTo to name, so it carries no secret.
func (i *instance) leak(host string, parts ...string) error {
	host = strings.ToLower(host)
	i.mu.RLock()
	bindings := i.bindings
	i.mu.RUnlock()
	if len(bindings) == 0 {
		return nil
	}
	var pieces []string
	for _, part := range parts {
		pieces = append(pieces, unpacked(part)...)
	}
	for _, b := range bindings {
		if host == broadcast {
			if slices.ContainsFunc(pieces, b.appearsIn) {
				return i.violate("secret", fmt.Sprintf("the secret setting %s can't be broadcast: a broadcast has no one host, so sendTo can't allow it", b.key))
			}
			continue
		}
		allowed := slices.ContainsFunc(b.hosts, func(h string) bool { return h == host || h == bareName(host) })
		if b.restricted && !allowed && slices.ContainsFunc(pieces, b.appearsIn) {
			return i.violate("secret", fmt.Sprintf("the secret setting %s can't be sent to %s: its sendTo doesn't list it", b.key, host))
		}
	}
	return nil
}

// scrub removes secrets, in every form leak looks for, from text about to be stored or shown. Text still holding one
// deeper down, inside JSON or as a stretch of a file, is replaced whole.
func (i *instance) scrub(text string) string {
	i.mu.RLock()
	bindings := i.bindings
	i.mu.RUnlock()
	for _, b := range bindings {
		for _, form := range b.forms {
			text = strings.ReplaceAll(text, form, "***")
		}
		text = strings.ReplaceAll(strings.ReplaceAll(text, b.hexForm, "***"), strings.ToUpper(b.hexForm), "***")
	}
	if i.holdsSecret(text) {
		return "*** (this held a secret setting)"
	}
	return text
}

// goError is an error for extension code to catch. NewGoError alone also hands over the live Go error as err.value, and
// with it whatever that holds, such as a failed database connection's password.
func goError(vm *goja.Runtime, err error) *goja.Object {
	e := vm.NewGoError(err)
	e.Delete("value")
	return e
}

// storeFailed tells the extension saved data is unavailable, and keeps the reason for the log: a store error can name
// the database's address and login.
func storeFailed(vm *goja.Runtime, id string, err error) {
	if err != nil {
		slog.Warn("extension storage failed", "extension", id, "err", err)
		panic(goError(vm, errors.New("saved data can't be changed right now")))
	}
}

// plain gives the VM its own copy of data. A Go map or slice stays live inside it, where a write reaches Go's own value
// and setting a slice's length allocates in one step.
func plain(vm *goja.Runtime, value any) goja.Value {
	data, err := json.Marshal(value)
	throw(vm, err)
	var copied any
	throw(vm, json.Unmarshal(data, &copied))
	return vmCopy(vm, copied)
}

// vmCopy builds the value in the VM itself, since its own JSON.parse is the extension's to replace.
func vmCopy(vm *goja.Runtime, value any) goja.Value {
	switch v := value.(type) {
	case map[string]any:
		object := vm.NewObject()
		for _, key := range sorted(v) {
			object.Set(key, vmCopy(vm, v[key]))
		}
		return object
	case []any:
		items := make([]any, len(v))
		for n, item := range v {
			items[n] = vmCopy(vm, item)
		}
		return vm.NewArray(items...)
	}
	return vm.ToValue(value)
}

var serverNameFormat = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)

// serverName is the name a TLS handshake sends: the host, or another the extension gave, which is data leaving like any other.
func (n *network) serverName(host, name string, allowPrivate bool) (string, error) {
	if name == "" || strings.EqualFold(name, host) {
		return host, nil
	}
	// On a shared CDN another name reaches another site through an approved address.
	if !allowPrivate {
		return "", n.violate("network", "tcp.connect: serverName must be the host itself on the internet")
	}
	if !serverNameFormat.MatchString(name) {
		return "", n.violate("network", "tcp.connect: serverName must be a host name")
	}
	if err := n.send(host, name); err != nil {
		return "", err
	}
	return name, nil
}

func bareName(hostPort string) string {
	if name, _, err := net.SplitHostPort(hostPort); err == nil {
		return name
	}
	return hostPort
}

// The VM's own ways to build code from text are replaced before the extension's code runs, so the scan of its source
// means something: code that never says eval or Function can't reach them either. instanceof Function keeps working.
const lockSource = `(function (blocked) {
  var stub = function () { blocked("the Function constructor"); };
  stub.prototype = Function.prototype;
  var set = function (target, name, value) {
    Object.defineProperty(target, name, { value: value, writable: false, configurable: false });
  };
  set(Function.prototype, "constructor", stub);
  set(Object.getPrototypeOf(function* () {}), "constructor", stub);
  set(Object.getPrototypeOf(async function () {}), "constructor", stub);
  set(globalThis, "Function", stub);
  set(globalThis, "eval", function () { blocked("eval"); });
})`

func (l *loaded) lockDynamicCode() error {
	vm := l.vm
	lock, err := vm.RunString(lockSource)
	if err != nil {
		return err
	}
	fn, _ := goja.AssertFunction(lock)
	_, err = fn(goja.Undefined(), vm.ToValue(func(what string) {
		panic(goError(vm, l.violate("dynamic-code", what+" isn't allowed in an extension")))
	}))
	return err
}
