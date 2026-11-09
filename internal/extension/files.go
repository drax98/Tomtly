package extension

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
)

const (
	maxSettingFile = 1 << 20
	minSecretField = 8 // anything shorter would match ordinary text and refuse honest requests
)

// readFileSettings returns settings with each file setting's path replaced by the file's text. os.Root keeps a path or
// a symlink from leading outside the config folder.
func readFileSettings(configDir string, declared map[string]Setting, settings map[string]any) (map[string]any, error) {
	out := maps.Clone(settings)
	var root *os.Root
	for key, s := range declared {
		path, _ := settings[key].(string)
		if s.Type != "file" || path == "" {
			continue
		}
		if root == nil {
			var err error
			if root, err = os.OpenRoot(configDir); err != nil {
				return nil, fmt.Errorf("the setting %q: can't open the config folder", key)
			}
			defer root.Close()
		}
		file, err := root.Open(path)
		if err != nil {
			return nil, fmt.Errorf("the setting %q: can't read %s inside your config folder", key, path)
		}
		data, err := io.ReadAll(io.LimitReader(file, maxSettingFile+1))
		file.Close()
		if err != nil {
			return nil, fmt.Errorf("the setting %q: can't read %s inside your config folder", key, path)
		}
		if len(data) > maxSettingFile {
			return nil, fmt.Errorf("the setting %q: %s is over 1 MiB", key, path)
		}
		out[key] = string(data)
	}
	return out, nil
}

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
