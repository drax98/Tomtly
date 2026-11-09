package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dop251/goja"
)

// storageObject is ctx.storage: values that survive a restart, like a rotating refresh token. It keeps a copy so reads
// don't touch the database, and a write that can't be saved fails the call rather than being lost quietly.
func (i *instance) storageObject(ctx context.Context, vm *goja.Runtime, id string, declared bool) map[string]any {
	st := i.manager.store
	values := map[string]json.RawMessage{}
	if declared {
		loaded, cancel := context.WithTimeout(ctx, storeTimeout)
		defer cancel()
		if saved, err := st.ExtensionStorage(loaded, id); err != nil {
			slog.Warn("extension storage unreadable", "extension", id, "err", err)
		} else {
			values = saved
		}
	}
	gate := func(what string) {
		if !declared {
			throw(vm, i.violate("storage", "storage."+what+" needs storage in access"))
		}
	}
	size := func() int {
		total := 0
		for key, value := range values {
			total += len(key) + len(value)
		}
		return total
	}
	return map[string]any{
		"get": func(key string) goja.Value {
			gate("get")
			raw, ok := values[key]
			if !ok {
				return goja.Undefined()
			}
			return parseJSON(vm, string(raw))
		},
		"set": func(key string, value goja.Value) {
			gate("set")
			text, err := stringify(vm, value)
			throw(vm, err)
			if text == "" {
				panic(goError(vm, errors.New("storage holds anything JSON can; this isn't")))
			}
			if size()-len(values[key])+len(key)+len(text) > maxStorage {
				panic(goError(vm, fmt.Errorf("storage is limited to %d bytes", maxStorage)))
			}
			saving, cancel := context.WithTimeout(ctx, storeTimeout)
			defer cancel()
			storeFailed(vm, id, st.SetExtensionValue(saving, id, key, json.RawMessage(text)))
			values[key] = json.RawMessage(text)
		},
		"delete": func(key string) {
			gate("delete")
			saving, cancel := context.WithTimeout(ctx, storeTimeout)
			defer cancel()
			storeFailed(vm, id, st.DeleteExtensionValue(saving, id, key))
			delete(values, key)
		},
	}
}
