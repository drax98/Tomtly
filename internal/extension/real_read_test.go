package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Read-only against a real home: it only polls and never calls Write. HOME_CONTROL_REAL_READ names the extension,
// HOME_CONTROL_REAL_SETTINGS holds its settings as JSON, and the result goes to HOME_CONTROL_REAL_OUT.
func TestRealRead(t *testing.T) {
	name := os.Getenv("HOME_CONTROL_REAL_READ")
	if name == "" {
		t.Skip("set HOME_CONTROL_REAL_READ to run against real devices")
	}
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(os.Getenv("HOME_CONTROL_REAL_SETTINGS")), &settings); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	m := run(t, example(t, name), settings, func(m *Manager) { m.configDir = filepath.Join(home, ".config", "home-control") })
	time.Sleep(30 * time.Second)
	data, err := json.MarshalIndent(m.Devices(), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("HOME_CONTROL_REAL_OUT"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := status(m); s.Error != "" || len(s.Violations) > 0 {
		t.Fatalf("error: %q, violations: %d", s.Error, len(s.Violations))
	}
}
