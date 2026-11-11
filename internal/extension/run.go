package extension

import (
	"context"

	"errors"
	"fmt"

	"log/slog"

	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"tomtly/device"
	"tomtly/internal/store"
)

const (
	callTimeout = 60 * time.Second // an endless loop is stopped; each request has its own, shorter limit
	maxOutput   = 1 << 20
)

// isPlugin says whether it publishes data or takes actions for scripts, which is what the plugin id is claimed for.
func (l *loaded) isPlugin() bool { return l.publishes() || len(l.actions) > 0 }

// publishes says whether it has data for scripts: fetched, or pushed with ctx.publish by a webhook or a background loop.
func (l *loaded) publishes() bool {
	return l.Data != nil || l.Webhooks != nil || (l.Background != nil && l.Devices == nil)
}

type pendingAction struct {
	name string
	args []byte
}

type pendingWrite struct {
	deviceID, code string
	value          any
}

// instance is one running extension.
const writeErrorShown = 5 * time.Minute

type instance struct {
	writeErrorUntil  time.Time // a failed command stays on the card this long, or until one works
	writeErrorDevice string
	declared         map[string]Setting // as this run defined them
	activity         activityLog
	manager          *Manager
	entry            entry
	writes           chan pendingWrite
	actions          chan pendingAction
	cancel           context.CancelFunc
	done             chan struct{}

	approve chan struct{} // wakes it when the owner approves, buffered so a wake-up isn't lost
	hooks   chan pendingHook
	finds   chan pendingFind
	refresh atomic.Bool // set by ctx.refreshDevices, and answered by the loop once the current call is over

	mu          sync.RWMutex
	status      Status
	dir         string
	bindings    []binding
	devices     []device.Device
	requested   []string // the access keys of what's loaded
	violations  int      // since the last approval
	quarantined bool
	stopRun     context.CancelFunc
	hooked      bool // it declared webhooks, and has access to them
	windowStart time.Time
	ops         int

	widgets map[string]widgetState // as last drawn, by key; under mu

	findMu  sync.Mutex // one network scan at a time, and whoever waited shares its result
	found   []foundDevice
	foundAt time.Time
}

func (i *instance) identity() (string, string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.status.ID, i.dir
}

func (i *instance) currentStatus() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.status
}

func (i *instance) currentDevices() []device.Device {
	i.mu.RLock()
	defer i.mu.RUnlock()
	devices := slices.DeleteFunc(slices.Clone(i.devices), func(d device.Device) bool { return i.manager.isRemoved(d.ID) })
	if time.Now().Before(i.writeErrorUntil) {
		for n := range devices {
			if devices[n].ID == i.writeErrorDevice {
				devices[n].Error = i.status.Error
			}
		}
	}
	return devices
}

func (i *instance) findDevice(id string) (device.Device, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, d := range i.devices {
		if d.ID == id && !i.manager.isRemoved(id) {
			return d, true
		}
	}
	return device.Device{}, false
}

func (i *instance) queueWrite(d device.Device, dp string, value any) error {
	for _, point := range d.DPs {
		if point.DP != dp {
			continue
		}
		if !point.Writable {
			return fmt.Errorf("%s is read-only", point.Code)
		}
		checked, err := device.Check(point.Type, point.Values, value)
		if err != nil {
			return err
		}
		select {
		case i.writes <- pendingWrite{strings.TrimPrefix(d.ID, d.Adapter+":"), point.Code, checked}:
			return nil
		default:
			return errors.New("too many commands are waiting")
		}
	}
	return fmt.Errorf("no such value %s", dp)
}

// plainError is the message a person needs, without the VM's stack, whose line numbers are of the compiled bundle.
func plainError(err error) string {
	var tooDeep *goja.StackOverflowError
	if errors.As(err, &tooDeep) {
		return "it called itself too many times"
	}
	if exception, ok := err.(*goja.Exception); ok {
		return strings.TrimPrefix(exception.Value().String(), "GoError: ")
	}
	return err.Error()
}

func (i *instance) fail(err error) {
	message := i.scrub(plainError(err))
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.quarantined {
		return // the reason it was stopped stays
	}
	i.status.Running, i.status.Error = false, message
	for j := range i.devices {
		i.devices[j].Online = false
	}
}

func (i *instance) setError(err error) {
	message := ""
	if err != nil {
		message = i.scrub(plainError(err))
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.quarantined {
		return
	}
	if message == "" && time.Now().Before(i.writeErrorUntil) {
		return // a good poll says nothing about the command that failed, and the owner hasn't seen why yet
	}
	if message != "" && i.status.Error != message {
		slog.Warn("extension error", "extension", i.status.ID, "err", message)
	}
	i.status.Error = message
}

// start loads the extension and runs it until ctx ends. It only returns early if it could not start.
func (i *instance) start(ctx context.Context) error {
	dir, err := i.manager.resolve(ctx, i.entry.Source)
	if err != nil {
		return err
	}
	l, err := loadWatched(dir, func(vm *goja.Runtime) func() { return i.manager.track(vm, nil) })
	if err != nil {
		return err
	}
	if err := i.manager.claim(l.ID); err != nil {
		return err
	}
	defer i.manager.release(l.ID)
	settings, settingsErr := resolveSettings(l.Settings, i.entry.Settings)
	i.describe(l, dir, settings)
	l.violate = i.violate
	i.resumeViolations(ctx, l.ID) // a restart must not reset the count toward being stopped
	for _, v := range l.early {
		i.violate(v[0], v[1])
	}
	i.guardSecrets(ctx, l, settings)
	if err := i.awaitApproval(ctx); err != nil {
		return err
	}
	if settingsErr != nil {
		return settingsErr
	}
	if l.isPlugin() {
		if err := i.manager.plugins.Claim(l.ID, "extension"); err != nil {
			return err
		}
		defer i.manager.plugins.Remove(l.ID)
		if len(l.actions) > 0 {
			i.manager.plugins.SetActions(l.ID, sorted(l.actions), func(name string, args []byte) error {
				select {
				case i.actions <- pendingAction{name, args}:
					return nil
				default:
					return errors.New("too many actions are waiting")
				}
			})
		}
	}
	running, stopRun := context.WithCancel(ctx)
	defer stopRun()
	if err := i.manager.loadRemoved(ctx); err != nil {
		return err // running without it would bring removed devices back, and let scripts command them
	}
	i.mu.Lock()
	i.stopRun = func() {
		stopRun()
		l.vm.Interrupt("extension was stopped") // now, not when the loop next looks
	}
	i.mu.Unlock()
	i.loop(running, l, settings)
	i.flushActivity()
	i.mu.Lock()
	i.stopRun = nil // it holds the VM, which can then be freed
	i.mu.Unlock()
	if running.Err() != nil && ctx.Err() == nil {
		// Stopped for breaking its access. Its data stays where it is, and nothing runs until the owner approves.
		select {
		case <-ctx.Done():
		case <-i.approve:
			return errRestart
		}
	}
	return ctx.Err()
}

func (i *instance) describe(l *loaded, dir string, settings map[string]any) {
	status := definedStatus(i.entry.Source, l, settings)
	status.Running = true
	i.mu.Lock()
	defer i.mu.Unlock()
	i.dir, i.bindings = dir, bindingsFor(l, settings)
	i.requested, i.declared = l.accessKeys(settings), l.Settings
	i.hooked = l.Webhooks != nil && l.access.Webhooks
	i.status = status
}

// definedStatus is what a definition says about an extension. settings only decide what counts as set.
func definedStatus(source string, l *loaded, settings map[string]any) Status {
	status := Status{Source: source, ID: l.ID, Name: l.Name, Version: l.Version, Description: l.Description, Homepage: l.Homepage, License: l.License,
		Categories: l.Categories, Tags: l.Tags,
		Network: append([]string{}, l.access.Network...), Settings: []SettingStatus{}, Provides: []string{},
		Access: l.accessLabels(settings), Warnings: append([]string{}, l.warnings...), Risks: l.riskWarnings(), Violations: []store.Violation{}}
	for _, key := range sorted(l.Settings) {
		s := l.Settings[key]
		given, set := settings[key]
		if s.Default != nil {
			set = true
		}
		if hiddenType(s.Type) {
			given = nil // the page is never told a secret or a file's contents
		}
		status.Settings = append(status.Settings, SettingStatus{key, s.Label, s.Type, s.Required, s.Help, s.Default, set, given})
	}
	if l.publishes() {
		status.Provides = append(status.Provides, "data")
	}
	if l.Webhooks != nil {
		status.Provides = append(status.Provides, "webhooks")
	}
	if l.Devices != nil {
		status.Provides = append(status.Provides, "devices")
	}
	if len(l.actions) > 0 {
		status.Provides = append(status.Provides, "actions")
	}
	if len(l.widgets) > 0 {
		status.Provides = append(status.Provides, "widgets")
		status.Widgets = l.widgetInfo()
	}
	return status
}

func (i *instance) loop(ctx context.Context, l *loaded, settings map[string]any) {
	api := i.context(ctx, l, settings)
	defer context.AfterFunc(ctx, func() { l.vm.Interrupt("extension was stopped") })()
	var dataTicks, deviceTicks, backgroundTicks <-chan time.Time
	if l.Data != nil {
		t := time.NewTicker(seconds(l.Data.EverySeconds))
		defer t.Stop()
		dataTicks = t.C
		i.fetchData(l, api)
	}
	if l.Devices != nil {
		t := time.NewTicker(seconds(l.Devices.EverySeconds))
		defer t.Stop()
		deviceTicks = t.C
		i.pollDevices(l, api)
	}
	var widgetTicks <-chan time.Time
	if len(l.widgets) > 0 {
		t := time.NewTicker(widgetEvery)
		defer t.Stop()
		widgetTicks = t.C
		i.renderWidgets(l, api)
	}
	var background *time.Timer
	if l.Background != nil {
		background = time.NewTimer(0)
		defer background.Stop()
		backgroundTicks = background.C
	}
	for {
		if ctx.Err() != nil {
			return // a select would pick at random between this and any call that is ready
		}
		if i.refresh.Swap(false) && l.Devices != nil {
			i.pollDevices(l, api)
		}
		select {
		case <-ctx.Done():
			return
		case <-dataTicks:
			i.fetchData(l, api)
		case <-deviceTicks:
			i.pollDevices(l, api)
		case <-backgroundTicks:
			wait := seconds(l.Background.EverySeconds)
			if err := i.runBackground(l, api); err != nil {
				wait = max(wait, time.Second) // a run that keeps failing doesn't get to spin
			}
			background.Reset(wait)
		case job := <-i.hooks:
			i.receive(l, api, job)
		case job := <-i.finds:
			job.done <- i.find(l, api)
		case <-widgetTicks:
			i.renderWidgets(l, api)
		case a := <-i.actions:
			_, err := i.call(l, l.actions[a.name], api, parseJSON(l.vm, string(a.args)))
			i.setError(err)
			if len(l.widgets) > 0 {
				i.renderWidgets(l, api) // a button shows what it did now, not at the next draw
			}
		case w := <-i.writes:
			if l.writeDevice == nil {
				i.setError(errors.New("this extension has no write function"))
				continue
			}
			_, err := i.call(l, l.writeDevice, api, l.vm.ToValue(w.deviceID), l.vm.ToValue(w.code), l.vm.ToValue(w.value))
			i.mu.Lock()
			i.writeErrorUntil, i.writeErrorDevice = time.Time{}, ""
			if err != nil {
				i.writeErrorUntil, i.writeErrorDevice = time.Now().Add(writeErrorShown), l.ID+":"+w.deviceID
			}
			i.mu.Unlock()
			i.setError(err)
			if err == nil {
				i.pollDevices(l, api) // show the result now, not at the next poll
			}
		}
	}
}
