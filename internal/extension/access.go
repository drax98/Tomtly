package extension

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

// approveAll skips the owner's approval. Only tests set it; the real flow is the point of the whole package.
var approveAll atomic.Bool

// errRestart means the extension was approved after being stopped, and starts again from its source.
var errRestart = errors.New("approved, restarting")

// accessDecl is what an extension asks for in defineExtension. Nothing is granted by default.
type accessDecl struct {
	Network []string `json:"network"`
	Lan     bool     `json:"lan"`
	Sockets bool     `json:"sockets"`
	Storage bool     `json:"storage"`
	// Webhooks gives the extension an address outside callers can send messages to.
	Webhooks bool `json:"webhooks"`
	// Listen is ports it may accept UDP datagrams or TCP connections on.
	Listen []int `json:"listen"`
}

// hostIn is the address a {setting} network entry reaches now, if it holds one.
func hostIn(settings map[string]any, name string) (string, bool) {
	value, ok := settings[name].(string)
	if !ok || !hostFormat.MatchString(value) {
		return "", false
	}
	return strings.ToLower(value), true
}

// hostGrant is the key for a {setting} entry. It holds the setting too: the same address under another name could change
// which address a sendTo secret goes to. A grant from before addresses were part of it, "network:{host}", never matches.
func hostGrant(entry, name string, settings map[string]any) (string, bool) {
	host, ok := hostIn(settings, name)
	return "network:" + entry + "=" + host, ok
}

// keys are the canonical strings stored as grants. An address counts, so a new one needs approving, and an unset one
// reaches nothing, so there is nothing to approve.
func (a accessDecl) keys(settings map[string]any) []string {
	keys := []string{}
	for _, entry := range a.Network {
		if m := hostSetting.FindStringSubmatch(entry); m != nil {
			if key, ok := hostGrant(entry, m[1], settings); ok {
				keys = append(keys, key)
			}
			continue
		}
		keys = append(keys, "network:"+entry)
	}
	if a.Lan {
		keys = append(keys, "lan")
	}
	if a.Sockets {
		keys = append(keys, "sockets")
	}
	if a.Storage {
		keys = append(keys, "storage")
	}
	if a.Webhooks {
		keys = append(keys, "webhooks")
	}
	for _, port := range a.Listen {
		keys = append(keys, "listen:"+strconv.Itoa(port))
	}
	return keys
}

// labels are the same, in words for the page. Marketplace listings hold them with no settings, so those words can't change.
func (a accessDecl) labels(settings map[string]any) []string {
	labels := []string{}
	for _, entry := range a.Network {
		if m := hostSetting.FindStringSubmatch(entry); m != nil {
			if host, ok := hostIn(settings, m[1]); ok {
				labels = append(labels, "Can reach "+host+" (the setting "+m[1]+"), which may be on your local network")
			} else {
				labels = append(labels, "Local network: the address in the setting "+m[1])
			}
		} else {
			labels = append(labels, "Internet: "+entry)
		}
	}
	if a.Lan {
		labels = append(labels, "Any device on your local network")
	}
	if a.Sockets {
		labels = append(labels, "Raw TCP and UDP connections")
	}
	if a.Storage {
		labels = append(labels, "Saved data")
	}
	if a.Webhooks {
		labels = append(labels, "Receive messages from outside")
	}
	for _, port := range a.Listen {
		labels = append(labels, "Accept connections from outside on port "+strconv.Itoa(port))
	}
	return labels
}

// secretGrant marks a setting that was a secret when approved. The definition is code, so a later run could call the same
// setting a plain string, which would take away every protection and show it on the page.
const secretGrant = "secret:"

// noLongerSecret is the settings approved as secrets that this definition doesn't treat as secrets.
func noLongerSecret(l map[string]Setting, grants []string) []string {
	var out []string
	for _, grant := range grants {
		if key, ok := strings.CutPrefix(grant, secretGrant); ok && l[key].Type != "secret" {
			out = append(out, key)
		}
	}
	return out
}

// guardSecrets keeps a setting approved as a secret protected, and reports it, until the owner approves the change.
func (i *instance) guardSecrets(ctx context.Context, l *loaded, values map[string]any) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	grants, err := i.manager.store.Grants(ctx, l.ID)
	if err != nil {
		return // granted refuses too, so nothing runs
	}
	for _, key := range noLongerSecret(l.Settings, grants) {
		i.mu.Lock()
		for n, s := range i.status.Settings {
			if s.Key == key {
				i.status.Settings[n].Value = nil
			}
		}
		i.status.Risks = append(i.status.Risks, "You approved the setting "+key+" as a secret, and this code no longer treats it as one. A rogue extension does this to get a key out. Approve only if you trust this change.")
		i.mu.Unlock()
		if value, ok := values[key].(string); ok && value != "" {
			i.mu.Lock()
			i.bindings = append(i.bindings, binding{key: key, forms: secretForms(value), hexForm: hex.EncodeToString([]byte(value)), restricted: true})
			i.mu.Unlock()
		}
		i.violate("secret", "the setting "+key+" was approved as a secret and is no longer declared as one")
	}
}

// accessKeys is what needs approving: what it can reach, and which of your secrets may go where.
func (l *loaded) accessKeys(settings map[string]any) []string {
	keys := l.access.keys(settings)
	for _, key := range sorted(l.Settings) {
		if s := l.Settings[key]; s.Type == "secret" {
			keys = append(keys, secretGrant+key)
			for _, entry := range s.SendTo {
				keys = append(keys, "send:"+key+":"+entry)
			}
		}
	}
	return keys
}

// accessLabels is everything the page says this extension can reach, with these settings.
func (l *loaded) accessLabels(settings map[string]any) []string {
	labels := l.access.labels(settings)
	for _, key := range sorted(l.Settings) {
		s := l.Settings[key]
		if s.Type == "file" {
			labels = append(labels, "Holds the file you give it for: "+s.Label)
		}
		if s.Type == "secret" {
			for _, entry := range s.SendTo {
				labels = append(labels, "Sends your "+s.Label+" to "+sendTarget(entry, settings))
			}
		}
	}
	return labels
}

func sendTarget(entry string, settings map[string]any) string {
	m := hostSetting.FindStringSubmatch(entry)
	if m == nil {
		return entry
	}
	if host, ok := hostIn(settings, m[1]); ok {
		return host
	}
	return "the address in the setting " + m[1]
}

var looksSecret = regexp.MustCompile(`(?i)api.?key|token|passw|secret|credential|\bkey\b`)

// riskWarnings says in plain words what approving could cost. The service recognises a secret as written or encoded, but
// code can scramble one first, so anywhere the extension can send data is somewhere a secret could end up. See leak.
func (l *loaded) riskWarnings() []string {
	warnings := []string{}
	for _, key := range sorted(l.Settings) {
		s := l.Settings[key]
		if s.Type != "secret" && s.Type != "file" {
			continue
		}
		out := []string{}
		for _, entry := range l.access.Network {
			if slices.Contains(s.SendTo, entry) {
				continue
			}
			if m := hostSetting.FindStringSubmatch(entry); m != nil {
				out = append(out, "the address in the setting "+m[1])
			} else {
				out = append(out, entry)
			}
		}
		if l.access.Webhooks {
			out = append(out, "anyone who calls its webhook address")
		}
		for _, port := range l.access.Listen {
			out = append(out, "anyone who connects to port "+strconv.Itoa(port))
		}
		if len(out) > 0 {
			warnings = append(warnings, "It holds your "+s.Label+" and can also send data to "+strings.Join(out, ", ")+". The service blocks that secret going there as it is, but not if the code scrambles it first. A rogue extension could steal it this way, so approve this only if you trust who wrote it.")
		}
		if l.access.Lan {
			warnings = append(warnings, "It holds your "+s.Label+" and can send data to any device on your local network, including your router, which may pass it on to the internet. Approve this only if you trust who wrote it.")
		}
	}
	for _, key := range sorted(l.Settings) {
		if s := l.Settings[key]; s.Type == "string" && looksSecret.MatchString(key+" "+s.Label) {
			warnings = append(warnings, "It asks for "+s.Label+" but doesn't treat it as a secret, so it shows on the page and nothing stops it being sent anywhere. Don't enter a real key or password here.")
		}
	}
	return warnings
}

// checkListen refuses ports an extension may never ask for, however the owner answers.
func (a accessDecl) checkListen() error {
	for n, port := range a.Listen {
		if port < 1 || port > 65535 {
			return fmt.Errorf("access.listen: %d must be from 1 to 65535", port)
		}
		if slices.Contains(a.Listen[:n], port) {
			return fmt.Errorf("access.listen: %d is listed twice", port)
		}
	}
	return nil
}

// granted says whether everything the loaded extension asks for was approved. An unreadable store grants nothing.
func (i *instance) granted(ctx context.Context) bool {
	i.mu.RLock()
	id, requested, quarantined, settingsOf := i.status.ID, i.requested, i.quarantined, i.declared
	i.mu.RUnlock()
	switch {
	case quarantined:
		return false
	case approveAll.Load():
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	grants, err := i.manager.store.Grants(ctx, id)
	if err != nil {
		slog.Warn("extension grants unreadable, so none are given", "extension", id, "err", err)
		return len(requested) == 0
	}
	if len(noLongerSecret(settingsOf, grants)) > 0 {
		return false
	}
	for _, key := range requested {
		if !slices.Contains(grants, key) {
			return false
		}
	}
	return true
}

// awaitApproval returns once everything requested is granted, and until then shows the extension as waiting for it.
func (i *instance) awaitApproval(ctx context.Context) error {
	for !i.granted(ctx) {
		i.mu.Lock()
		i.status.Running, i.status.NeedsApproval = false, true
		i.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-i.approve:
		}
	}
	i.mu.Lock()
	i.status.Running, i.status.NeedsApproval = true, false
	i.mu.Unlock()
	return nil
}

// approveTypedHosts moves the approval of an address the person just typed on the page, since typing it is approving it.
// Only for what's approved now: it must not wake an extension waiting for approval, or one stopped for its violations.
func (m *Manager) approveTypedHosts(ctx context.Context, inst *instance, l *loaded, before, after, given map[string]any) error {
	if !inst.granted(ctx) {
		return nil
	}
	grants, err := m.store.Grants(ctx, l.ID)
	if err != nil {
		return err
	}
	moved := slices.Clone(grants)
	for _, entry := range l.access.Network {
		match := hostSetting.FindStringSubmatch(entry)
		if match == nil {
			continue
		}
		if _, typed := given[match[1]]; !typed {
			continue
		}
		if key, ok := hostGrant(entry, match[1], before); ok {
			moved = slices.DeleteFunc(moved, func(g string) bool { return g == key })
		}
		if key, ok := hostGrant(entry, match[1], after); ok && !slices.Contains(moved, key) {
			moved = append(moved, key)
		}
	}
	if slices.Equal(grants, moved) {
		return nil
	}
	return m.store.SetGrants(ctx, l.ID, moved)
}

// errAccessChanged says a definition answered differently from what the person agreed to, which is either a new version or
// code that branches on the moment it's asked.
var errAccessChanged = errors.New("what it asks for isn't what you were shown, so nothing was changed. Look at it again")

// sameAccess compares what was shown with what is asked for now, in any order.
func sameAccess(seen, now []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(seen)), slices.Sorted(slices.Values(now)))
}

// Approve grants an extension the access it asks for, as long as that is what the person was shown, forgets its violations
// and lets it run. If that made its first webhook address, it is returned: it's shown once, since only its hash is kept.
func (m *Manager) Approve(source string, seen []string) (webhook string, err error) {
	m.mu.RLock()
	var found *instance
	for _, inst := range m.instances {
		if inst.entry.Source == source {
			found = inst
		}
	}
	m.mu.RUnlock()
	if found == nil {
		return "", errors.New("no extension from that source")
	}
	found.mu.RLock()
	id, requested, waiting, labels := found.status.ID, found.requested, found.status.NeedsApproval, found.status.Access
	found.mu.RUnlock()
	if !waiting {
		return "", errors.New("this extension isn't waiting for approval")
	}
	if !sameAccess(seen, labels) {
		return "", errAccessChanged
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if webhook, err = m.firstWebhook(ctx, id, requested); err != nil {
		return "", fmt.Errorf("could not make its webhook address: %w", err)
	}
	if err := m.store.SetGrants(ctx, id, requested); err != nil {
		return "", fmt.Errorf("could not save the approval: %w", err)
	}
	if err := m.store.ClearViolations(ctx, id); err != nil {
		return "", fmt.Errorf("could not clear the violations: %w", err)
	}
	found.mu.Lock()
	found.violations, found.quarantined = 0, false
	found.mu.Unlock()
	select {
	case found.approve <- struct{}{}:
	default: // one wake-up is already waiting
	}
	return webhook, nil
}
