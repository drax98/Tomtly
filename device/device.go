// Package device is the vendor-neutral model every adapter exposes and everything above adapters uses.
package device

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

// Data point types.
const (
	Boolean = "Boolean"
	Enum    = "Enum"
	Integer = "Integer"
	Action  = "Action"
)

// DP is one readable or writable value on a device.
type DP struct {
	DP       string         `json:"dp"`
	Code     string         `json:"code"`
	Type     string         `json:"type"`
	Values   map[string]any `json:"values"`
	Scale    int            `json:"scale"`
	Writable bool           `json:"writable"`
	Value    any            `json:"value"`
	// Advanced is a setting rarely changed, such as a calibration: the page folds it away under the everyday values.
	Advanced bool `json:"advanced,omitempty"`
}

// Device is one device as the API, page and automations see it. It must never carry credentials.
type Device struct {
	ID       string `json:"id"`
	Adapter  string `json:"adapter"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // climate, sensor, light, plug, cover, vacuum, air, fan, safety, infrastructure, other
	Category string `json:"category"`
	Product  string `json:"product"`
	Online   bool   `json:"online"`
	DPs      []DP   `json:"dps"`
	// Error is why the last command to it failed, if that's recent. A queued command has already been answered yes, so this is the only place it can say.
	Error string `json:"error,omitempty"`
}

// Adapter connects one device family. Write validates and queues; it returns an error for a bad write.
type Adapter interface {
	Devices() []Device
	Write(id, dp string, value any) error
}

// Check validates value against a data point and returns it in canonical form: bool, string or int64.
func Check(typ string, values map[string]any, value any) (any, error) {
	switch typ {
	case Boolean:
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("expected true or false")
		}
		return value, nil
	case Enum, Action:
		s, ok := value.(string)
		if !ok || !slices.Contains(Strings(values["range"]), s) {
			return nil, fmt.Errorf("expected one of %v", values["range"])
		}
		return s, nil
	case Integer:
		n, ok := Number(value)
		if !ok || n != math.Trunc(n) {
			return nil, fmt.Errorf("expected an integer in raw device units")
		}
		if lo, ok := Number(values["min"]); ok && n < lo {
			return nil, fmt.Errorf("expected at least %v", lo)
		}
		if hi, ok := Number(values["max"]); ok && n > hi {
			return nil, fmt.Errorf("expected at most %v", hi)
		}
		return int64(n), nil
	}
	return nil, fmt.Errorf("type %s is not writable", typ)
}

// Number reads any numeric form: JSON gives float64, scripts give int64, tables give int.
func Number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// Strings reads a string list from either a Go table or decoded JSON.
func Strings(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Equal compares two values regardless of numeric representation.
func Equal(a, b any) bool {
	if x, ok := Number(a); ok {
		y, ok := Number(b)
		return ok && x == y
	}
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(x) == string(y)
}
