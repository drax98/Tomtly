// Package automation runs user scripts written in TypeScript against the vendor-neutral device model.
//
// Scripts run in shadow mode until switched live: they compute what they would do, and differences from what the
// devices actually do are reported, but nothing is sent. Each script has its own VM, so one bad script can't break
// the others, and it can only reach what the `home` object offers.
package automation

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"tomtly/device"
	"tomtly/internal/store"
	"tomtly/internal/tsbuild"
	"tomtly/plugin"
)

const (
	callTimeout   = 2 * time.Second  // a script that takes longer is stopped
	resendAfter   = 30 * time.Second // a live command that hasn't taken effect is sent again
	mismatchAfter = 60 * time.Second // a shadow decision the devices still disagree with is flagged
	setupRetry    = 30 * time.Second // a script that failed to load is tried again, in case its device appears
	startupGrace  = 30 * time.Second // after a start, integrations are still connecting, so a load failure is retried quietly
	maxEvents     = 200
	maxCallStack  = 4096
	storeTimeout  = 5 * time.Second
)

// Event is one entry in the activity log.
type Event = store.Event

// ScriptStatus is what the API reports for a script.
type ScriptStatus struct {
	Name  string `json:"name"`
	Live  bool   `json:"live"`
	Error string `json:"error,omitempty"`
	// Access is everything the script asked for, and whether the owner approved it.
	Access []Access `json:"access"`
}

// Status is the engine's state for the API.
type Status struct {
	Scripts []ScriptStatus  `json:"scripts"`
	Events  []Event         `json:"events"`
	Plugins []plugin.Status `json:"plugins"`
}

// Hub is what the engine needs from the device layer.
type Hub interface {
	Devices() []device.Device
	Write(id, dp string, value any) error
}

type dailyJob struct {
	at      string // HH:MM
	fn      goja.Callable
	lastDay string
}

type dpKey struct{ id, dp string }

type sentCommand struct {
	value any
	at    time.Time
}

type shadowDecision struct {
	value   any
	label   string
	at      time.Time
	flagged bool
}

type script struct {
	name       string
	vm         *goja.Runtime
	reconciles []goja.Callable
	dailies    []*dailyJob
	widgets    []*scriptWidget
	renderedAt time.Time // when its widgets were last drawn
	sent       map[dpKey]sentCommand
	acted      map[string]time.Time // recent actions, so one asked for in reconcile doesn't fire every second
	shadow     map[dpKey]*shadowDecision
	err        string
	dry        bool                // a trial load: nothing reaches a device or the log
	failedAt   time.Time           // when it last failed to load; zero if it loaded
	unreported bool                // its load failure came during startup and isn't logged yet
	requests   map[string]*request // what it asked for through the gate, by access key
	flooded    bool                // it asked for too many things, and was told once
	logs       int                 // lines logged since logStart
	logStart   time.Time
}

// request is one thing a script asked for.
type request struct{ refused int }

// Plugins is the outside data scripts can read.
type Plugins interface {
	Get(id string) (data []byte, updatedAt time.Time, ok bool)
	Statuses() []plugin.Status
	Act(id, name string, args []byte) error
}

// Engine loads scripts and runs them on one goroutine.
type Engine struct {
	// Plugins is set before Run, and may stay nil.
	Plugins Plugins

	hub      Hub
	dir      string
	sdkDir   string
	store    store.Store
	saving   chan store.Event // waiting for the database, so a slow one can't hold up the heating
	location *time.Location
	now      func() time.Time

	mu         sync.Mutex
	scripts    []*script
	live       map[string]bool
	grants     map[string]map[string]bool // what the owner approved, by script and access key
	events     []Event
	devices    map[string]device.Device
	seen       map[string]time.Time // when each device was last online
	quietUntil time.Time            // end of the startup grace; zero outside Run
}

// New returns an engine for the scripts in dir, keeping its state in st.
func New(hub Hub, dir, sdkDir string, st store.Store, location *time.Location) *Engine {
	e := &Engine{hub: hub, dir: dir, sdkDir: sdkDir, store: st, saving: make(chan store.Event, maxEvents), location: location, now: time.Now, live: map[string]bool{}, grants: map[string]map[string]bool{}, seen: map[string]time.Time{}}
	e.loadState()
	return e
}

// loadState restores which scripts are live and the activity log. Anything unreadable leaves them all in shadow mode,
// the safe side.
func (e *Engine) loadState() {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	live, err := e.store.ScriptsLive(ctx)
	if err != nil {
		slog.Warn("automation state unreadable, all scripts stay in shadow mode", "err", err)
	} else {
		e.live = live
	}
	if grants, err := e.store.ScriptGrants(ctx); err != nil {
		slog.Warn("script approvals unreadable, so none are given", "err", err)
	} else {
		for script, keys := range grants {
			e.grants[script] = map[string]bool{}
			for _, key := range keys {
				e.grants[script][key] = true
			}
		}
	}
	if events, err := e.store.RecentEvents(ctx, maxEvents); err != nil {
		slog.Warn("activity log unreadable", "err", err)
	} else {
		e.events = events
	}
}

func (e *Engine) saveLive(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	return e.store.SetScriptLive(ctx, name, e.live[name])
}

// saveEvents writes the activity log in the background until stop closes.
func (e *Engine) saveEvents(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case event := <-e.saving:
			batch := []store.Event{event}
			for len(batch) < maxEvents && len(e.saving) > 0 {
				batch = append(batch, <-e.saving)
			}
			ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
			if err := e.store.AppendEvents(ctx, batch); err != nil {
				slog.Warn("activity log not saved", "err", err)
			}
			cancel()
		}
	}
}

// SetLive switches a script between shadow and live, and remembers it across restarts.
func (e *Engine) SetLive(name string, live bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !slices.ContainsFunc(e.scripts, func(s *script) bool { return s.name == name }) {
		return fmt.Errorf("unknown script %s", name)
	}
	previous, had := e.live[name]
	e.live[name] = live
	if err := e.saveLive(name); err != nil {
		if had {
			e.live[name] = previous
		} else {
			delete(e.live, name)
		}
		return fmt.Errorf("could not save the setting: %w", err)
	}
	for _, s := range e.scripts {
		if s.name == name {
			s.sent, s.shadow = map[dpKey]sentCommand{}, map[dpKey]*shadowDecision{}
		}
	}
	mode := "shadow"
	if live {
		mode = "live"
	}
	e.record(name, "log", "switched to "+mode)
	return nil
}

// Reload rebuilds every script from disk, so edits apply without a restart.
func (e *Engine) Reload() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.loadScripts()
}

func (e *Engine) loadScripts() {
	e.refreshDevices()
	e.scripts = nil
	files, err := filepath.Glob(filepath.Join(e.dir, "*.ts"))
	if err != nil {
		slog.Error("automation scripts unreadable", "err", err)
		return
	}
	for _, file := range files {
		if strings.HasSuffix(file, ".d.ts") {
			continue
		}
		e.scripts = append(e.scripts, e.load(strings.TrimSuffix(filepath.Base(file), ".ts")))
	}
}

// load builds a script from its file. A failure is kept on the script, so it shows on the page.
func (e *Engine) load(name string) *script {
	s := &script{name: name, sent: map[dpKey]sentCommand{}, shadow: map[dpKey]*shadowDecision{}, acted: map[string]time.Time{}, requests: map[string]*request{}}
	data, err := readScript(filepath.Join(e.dir, name+".ts"))
	if err == nil {
		// A failure here shows again in setup. What the trial found is the page's first answer to what this script needs.
		s.requests, _ = e.check(name, string(data))
		err = e.setup(s, string(data))
	}
	if err != nil {
		s.err = err.Error()
		s.reconciles, s.dailies, s.widgets = nil, nil, nil
		s.failedAt = e.now()
		if e.now().Before(e.quietUntil) {
			s.unreported = true
		} else {
			e.reportLoadFailure(s)
		}
	}
	return s
}

func (e *Engine) reportLoadFailure(s *script) {
	s.unreported = false
	e.record(s.name, "error", s.err)
	slog.Error("automation script failed to load", "script", s.name, "err", s.err)
}

// replaceScript swaps in a script's new version, or adds it if it is new, keeping the page's order by name.
func (e *Engine) replaceScript(name string) {
	loaded := e.load(name)
	for i, s := range e.scripts {
		if s.name == name {
			e.scripts[i] = loaded
			return
		}
	}
	e.scripts = append(e.scripts, loaded)
	slices.SortFunc(e.scripts, func(a, b *script) int { return strings.Compare(a.name, b.name) })
}

func (e *Engine) setup(s *script, source string) error {
	code, err := tsbuild.BundleScript(e.dir, s.name, source)
	if err != nil {
		return err
	}
	s.vm = goja.New()
	s.vm.SetTimeSource(e.now)
	s.vm.SetMaxCallStackSize(maxCallStack) // recursion through native calls grows the Go stack, and overflowing that kills the service
	if err := s.vm.Set("home", e.homeObject(s)); err != nil {
		return err
	}
	return e.guard(s, func() error {
		_, err := s.vm.RunString(code)
		return err
	})
}

// guard runs script code with a time limit, so an endless loop can't freeze the engine.
func (e *Engine) guard(s *script, fn func() error) error {
	timer := time.AfterFunc(callTimeout, func() { s.vm.Interrupt("script took too long") })
	defer timer.Stop()
	err := fn()
	s.vm.ClearInterrupt()
	return err
}

func (e *Engine) refreshDevices() {
	e.devices = map[string]device.Device{}
	for _, d := range e.hub.Devices() {
		e.devices[d.ID] = d
		// A device not yet heard from counts from the first time we look, so a restart starts the clock fresh.
		if _, known := e.seen[d.ID]; d.Online || !known {
			e.seen[d.ID] = e.now()
		}
	}
}

func (e *Engine) record(name, kind, message string) {
	if len(message) > maxMessage {
		message = message[:maxMessage] + "..."
	}
	// Postgres refuses these, and one bad line would lose the whole batch of log entries around it.
	message = strings.ReplaceAll(strings.ToValidUTF8(message, ""), "\x00", "")
	event := Event{Time: e.now(), Script: name, Kind: kind, Message: message}
	e.events = append(e.events, event)
	select {
	case e.saving <- event:
	default: // the database has fallen behind; this entry is only kept in memory
	}
	if len(e.events) > maxEvents {
		e.events = e.events[len(e.events)-maxEvents:]
	}
	if kind == "set" || kind == "error" || kind == "mismatch" || kind == "refused" {
		slog.Info("automation", "script", name, "kind", kind, "message", message)
	}
}

// Tick runs every script once against the latest device state.
func (e *Engine) Tick() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshDevices()
	now := e.now().In(e.location)
	hhmm, day := now.Format("15:04"), now.Format("2006-01-02")
	for i, s := range e.scripts {
		starting := e.now().Before(e.quietUntil)
		if !s.failedAt.IsZero() && (starting || e.now().Sub(s.failedAt) > setupRetry) {
			s = e.retry(s)
			e.scripts[i] = s
		}
		if s.unreported && !starting {
			e.reportLoadFailure(s)
		}
		for _, job := range s.dailies {
			// shortcut: a time missed while the service was down is skipped, not caught up; add catch-up if restarts near a schedule become a problem.
			if job.at == hhmm && job.lastDay != day {
				job.lastDay = day
				e.call(s, job.fn)
			}
		}
		for _, fn := range s.reconciles {
			e.call(s, fn)
		}
		if len(s.widgets) > 0 && e.now().Sub(s.renderedAt) >= widgetEvery {
			s.renderedAt = e.now()
			e.renderWidgets(s)
		}
		e.checkShadow(s)
	}
}

// retry loads a failed script again. The failure is only reported once, so a device that never appears isn't noisy.
func (e *Engine) retry(s *script) *script {
	data, err := readScript(filepath.Join(e.dir, s.name+".ts"))
	if err == nil {
		_, err = e.check(s.name, string(data))
	}
	if err != nil {
		s.failedAt = e.now()
		return s
	}
	return e.load(s.name)
}

func (e *Engine) call(s *script, fn goja.Callable) {
	err := e.guard(s, func() error {
		_, err := fn(goja.Undefined())
		return err
	})
	if err == nil {
		if s.err != "" {
			s.err = ""
		}
		return
	}
	if msg := err.Error(); s.err != msg { // report a repeating failure once
		s.err = msg
		e.record(s.name, "error", msg)
	}
}

// checkShadow reports whether shadow decisions came true, which is the evidence for switching a script live.
func (e *Engine) checkShadow(s *script) {
	for key, d := range s.shadow {
		current, known := e.valueOf(key)
		if known && device.Equal(current, d.value) {
			if d.flagged {
				e.record(s.name, "matched", d.label+" now matches")
			}
			delete(s.shadow, key)
			continue
		}
		if !d.flagged && e.now().Sub(d.at) > mismatchAfter {
			d.flagged = true
			e.record(s.name, "mismatch", fmt.Sprintf("%s: script wants this but the device still differs after %s", d.label, mismatchAfter))
		}
	}
}

func (e *Engine) valueOf(key dpKey) (any, bool) {
	d, ok := e.devices[key.id]
	if !ok {
		return nil, false
	}
	for _, dp := range d.DPs {
		if dp.DP == key.dp {
			return dp.Value, dp.Value != nil
		}
	}
	return nil, false
}

// Status returns the scripts and recent activity, newest first.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Status{Scripts: []ScriptStatus{}, Events: make([]Event, len(e.events)), Plugins: []plugin.Status{}}
	if e.Plugins != nil {
		st.Plugins = e.Plugins.Statuses()
	}
	for _, s := range e.scripts {
		st.Scripts = append(st.Scripts, ScriptStatus{Name: s.name, Live: e.live[s.name], Error: s.err, Access: e.accessOf(s)})
	}
	for i, ev := range e.events {
		st.Events[len(e.events)-1-i] = ev
	}
	return st
}

// Run loads the scripts and ticks once a second until stop closes.
func (e *Engine) Run(stop <-chan struct{}) {
	e.mu.Lock()
	e.quietUntil = e.now().Add(startupGrace)
	e.mu.Unlock()
	e.Reload()
	go e.saveEvents(stop)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			e.Tick()
		}
	}
}
