package extension

import (
	"encoding/json"
)

const (
	maxSettingFile = 1 << 20
	minSecretField = 8 // anything shorter would match ordinary text and refuse honest requests
)

// secretFieldValues finds every string under the named fields, at any depth of a JSON file.
func secretFieldValues(text string, fields []string) []string {
	var doc any
	if len(fields) == 0 || json.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for name, item := range v {
				for _, field := range fields {
					if s, ok := item.(string); ok && name == field && len(s) >= minSecretField {
						out = append(out, s)
					}
				}
				walk(item)
			}
		}
	}
	walk(doc)
	return out
}
