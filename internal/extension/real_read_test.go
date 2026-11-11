package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Read-only against a real home: it only polls and never calls Write. TOMTLY_REAL_READ is the extension's folder,
// TOMTLY_REAL_SETTINGS holds its settings as JSON, and the result goes to TOMTLY_REAL_OUT.
func TestRealRead(t *testing.T) {
	dir := os.Getenv("TOMTLY_REAL_READ")
	if dir == "" {
		t.Skip("set TOMTLY_REAL_READ to an extension folder to run against real devices")
	}
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(os.Getenv("TOMTLY_REAL_SETTINGS")), &settings); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	m := run(t, dir, settings, func(m *Manager) { m.configDir = filepath.Join(home, ".config", "tomtly") })
	time.Sleep(30 * time.Second)
	data, err := json.MarshalIndent(m.Devices(), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("TOMTLY_REAL_OUT"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := status(m); s.Error != "" || len(s.Violations) > 0 {
		t.Fatalf("error: %q, violations: %d", s.Error, len(s.Violations))
	}
}
