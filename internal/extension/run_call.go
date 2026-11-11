package extension

import (
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

func (i *instance) runBackground(l *loaded, api goja.Value) error {
	_, err := i.call(l, l.background, api)
	i.setError(err)
	return err
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// call runs extension code under a time limit. Requests are bounded separately, since this can't interrupt Go code.
func (i *instance) call(l *loaded, fn goja.Callable, args ...goja.Value) (goja.Value, error) {
	return i.callWithin(callTimeout, l, fn, args...)
}

func (i *instance) callWithin(limit time.Duration, l *loaded, fn goja.Callable, args ...goja.Value) (out goja.Value, err error) {
	i.guarded(l, limit, func() { out, err = fn(goja.Undefined(), args...) })
	return out, err
}

// guarded runs extension code under a time limit, with the memory watchdog told who is running. The timer stops before
// the interrupt is cleared, so a late one can't hit the next call.
func (i *instance) guarded(l *loaded, limit time.Duration, run func()) {
	timer := time.AfterFunc(limit, func() { l.vm.Interrupt("extension took too long") })
	stopTracking := i.manager.track(l.vm, i)
	defer func() {
		timer.Stop()
		stopTracking()
		l.vm.ClearInterrupt()
	}()
	run()
}

// output turns what extension code returned into JSON, refusing anything too large or holding a secret.
func (i *instance) output(l *loaded, value goja.Value) (string, error) {
	// A toJSON or getter runs here, so this is extension code like any other call.
	var text string
	var err error
	i.guarded(l, callTimeout, func() { text, err = stringify(l.vm, value) })
	if err != nil {
		return "", err
	}
	if len(text) > maxOutput {
		return "", fmt.Errorf("returned over %d bytes", maxOutput)
	}
	if i.holdsSecret(text) {
		return "", errors.New("returned one of its secret settings, which the API would show to anyone")
	}
	return text, nil
}

func (i *instance) fetchData(l *loaded, api goja.Value) {
	var text string
	value, err := i.call(l, l.fetchData, api)
	if err == nil {
		text, err = i.output(l, value)
	}
	if err == nil && text == "" {
		err = errors.New("data.fetch returned nothing")
	}
	if err != nil {
		err = errors.New(i.scrub(plainError(err)))
	}
	i.manager.plugins.Publish(l.ID, []byte(text), err)
	i.setError(err)
}
