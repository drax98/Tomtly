package extension

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dop251/goja"
)

// context builds the ctx object passed to an extension's functions: all it can do outside its VM.
func (i *instance) context(ctx context.Context, l *loaded, settings map[string]any) goja.Value {
	vm := l.vm
	net := newNetwork(l.access, settings, i.manager.allowLoopback)
	net.own = i.manager.own
	net.onViolation, net.onSpend, net.onSend, net.onUse = i.violate, i.spend, i.leak, i.note
	open := &connSet{conns: map[io.Closer]bool{}}
	context.AfterFunc(ctx, open.closeAll)
	return vm.ToValue(map[string]any{
		"settings": plain(vm, settings),
		"udp":      net.udpObject(ctx, vm, open),
		"tcp":      i.tcpObject(ctx, vm, net, open),
		"crypto":   cryptoObject(vm),
		"storage":  i.storageObject(ctx, vm, l.ID, l.access.Storage),
		"publish": func(value goja.Value) {
			if !l.publishes() {
				throw(vm, i.violate("publish", "publish needs data, webhooks, or background without devices, which make this a plugin"))
			}
			text, err := i.output(l, value)
			throw(vm, err)
			if text == "" {
				panic(goError(vm, errors.New("publish takes anything JSON can hold")))
			}
			i.manager.plugins.Publish(l.ID, []byte(text), nil)
		},
		"refreshDevices": func() {
			if l.Devices == nil {
				panic(goError(vm, errors.New("refreshDevices needs devices")))
			}
			i.refresh.Store(true)
		},
		"removedDevices": func() goja.Value {
			if l.Devices == nil {
				panic(goError(vm, errors.New("removedDevices needs devices")))
			}
			text, err := json.Marshal(i.manager.removedIDs(l.ID))
			throw(vm, err)
			return parseJSON(vm, string(text)) // a real array, which it can change without affecting anything
		},
		"log": func(message string) {
			slog.Info("extension", "extension", l.ID, "message", i.scrub(message))
		},
		"fetch": func(call goja.FunctionCall) goja.Value {
			var options struct {
				Method  string            `json:"method"`
				Headers map[string]string `json:"headers"`
				Body    string            `json:"body"`
				BodyHex string            `json:"bodyHex"`
			}
			if arg := call.Argument(1); !goja.IsUndefined(arg) {
				text, err := stringify(vm, arg)
				if err == nil {
					err = json.Unmarshal([]byte(text), &options)
				}
				if err != nil {
					panic(goError(vm, fmt.Errorf("fetch options: %w", err)))
				}
			}
			method := http.MethodGet
			if options.Method != "" {
				method = strings.ToUpper(options.Method)
			}
			body := options.Body
			if options.BodyHex != "" {
				body = string(unhex(vm, options.BodyHex))
			}
			r, err := net.do(ctx, call.Argument(0).String(), method, options.Headers, body)
			if err != nil {
				panic(goError(vm, err))
			}
			return vm.ToValue(map[string]any{
				"status": r.status,
				"ok":     r.status >= 200 && r.status < 300,
				"text":   r.body,
				"hex":    func() string { return hex.EncodeToString([]byte(r.body)) },
				"header": func(name string) string { return r.header.Get(name) },
				"json":   func() goja.Value { return parseJSON(vm, r.body) },
			})
		},
	})
}
