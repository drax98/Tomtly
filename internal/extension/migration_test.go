package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrationFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, "migrations", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadMigrationsSortsByVersionNotName(t *testing.T) {
	got, err := ReadMigrations(migrationFolder(t, map[string]string{
		"1.10.0.json": `{"version":"1.10.0","note":"later"}`,
		"1.2.0.json":  `{"version":"1.2.0","note":"earlier"}`,
	}))
	if err != nil || len(got) != 2 || got[0].Version != "1.2.0" {
		t.Fatalf("%v, %+v", err, got)
	}
	if none, err := ReadMigrations(t.TempDir()); err != nil || len(none) != 0 {
		t.Fatalf("no folder should be fine: %v, %v", none, err)
	}
}

func TestReadMigrationsExplainsBadFiles(t *testing.T) {
	for want, file := range map[string]map[string]string{
		"named <version>.json": {"notes.txt": `{}`},
		"says its version":     {"1.0.0.json": `{"version":"2.0.0","note":"x"}`},
		"needs a note":         {"1.0.0.json": `{"version":"1.0.0"}`},
		"unknown field":        {"1.0.0.json": `{"version":"1.0.0","note":"x","asks":["a"]}`},
		"isn't a version":      {"latest.json": `{"version":"latest","note":"x"}`},
	} {
		if _, err := ReadMigrations(migrationFolder(t, file)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestPlanUpgrade(t *testing.T) {
	migrations := []Migration{
		{Version: "1.0.0", Note: "already applied", Ask: []string{"never"}},
		{Version: "1.1.0", Note: "the host setting has a new name", Rename: map[string]string{"address": "host"}},
		{Version: "1.2.0", Note: "needs a token", Ask: []string{"pat"}},
		{Version: "1.3.0", Note: "old keys stopped working", Ask: []string{"key"}, Remove: []string{"key", "gone"}},
		{Version: "2.0.0", Note: "too new", Ask: []string{"later"}},
	}
	declared := []SettingStatus{
		{Key: "host", Type: "string", Required: true},
		{Key: "key", Type: "secret", Required: true},
		{Key: "pat", Type: "secret", Required: true},
		{Key: "plain", Type: "string"},
		{Key: "later", Type: "string"},
		{Key: "never", Type: "string"},
	}
	plan, settings := PlanUpgrade("1.0.0", "1.3.0", migrations, declared, map[string]any{"address": "1.2.3.4", "key": "old", "gone": "x", "stale": "y", "plain": "z"})
	if len(plan.Steps) != 3 || plan.Steps[0].Version != "1.1.0" || plan.Steps[2].Version != "1.3.0" {
		t.Fatalf("steps: %+v", plan.Steps)
	}
	if settings["host"] != "1.2.3.4" || settings["plain"] != "z" {
		t.Errorf("carried: %v", settings)
	}
	for _, key := range []string{"address", "key", "gone", "stale"} {
		if _, ok := settings[key]; ok {
			t.Errorf("%s should be gone: %v", key, settings)
		}
	}
	var asked []string
	for _, s := range plan.Ask {
		asked = append(asked, s.Key)
		if s.Set {
			t.Errorf("%s has no value left", s.Key)
		}
	}
	if strings.Join(asked, ",") != "key,pat" {
		t.Errorf("asked: %v", asked)
	}
}

func TestPlanUpgradeNeverReturnsASecret(t *testing.T) {
	plan, _ := PlanUpgrade("1.0.0", "1.1.0", []Migration{{Version: "1.1.0", Note: "x", Ask: []string{"pat", "host"}}},
		[]SettingStatus{{Key: "pat", Type: "secret"}, {Key: "host", Type: "string"}}, map[string]any{"pat": "sekret", "host": "a"})
	if plan.Ask[0].Value != nil || !plan.Ask[0].Set {
		t.Errorf("secret: %+v", plan.Ask[0])
	}
	if plan.Ask[1].Value != "a" {
		t.Errorf("plain value: %+v", plan.Ask[1])
	}
}

func TestARequiredSettingNothingAskedForIsStillAsked(t *testing.T) {
	plan, _ := PlanUpgrade("1.0.0", "1.1.0", nil, []SettingStatus{{Key: "pat", Type: "secret", Required: true}, {Key: "opt", Type: "string"}}, nil)
	if len(plan.Ask) != 1 || plan.Ask[0].Key != "pat" {
		t.Fatalf("%+v", plan.Ask)
	}
}

func TestCheckMigrations(t *testing.T) {
	before := []SettingStatus{{Key: "host", Required: true}}
	after := []SettingStatus{{Key: "host", Required: true}, {Key: "pat", Required: true}}
	if err := CheckMigrations("1.0.0", "1.1.0", nil, before, after); err == nil || !strings.Contains(err.Error(), `"pat"`) {
		t.Errorf("missing migration: %v", err)
	}
	if err := CheckMigrations("1.0.0", "1.1.0", []Migration{{Version: "1.1.0", Note: "x", Ask: []string{"pat"}}}, before, after); err != nil {
		t.Error(err)
	}
	if err := CheckMigrations("1.0.0", "1.1.0", []Migration{{Version: "1.1.0", Note: "x", Ask: []string{"nope", "pat"}}}, before, after); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("unknown setting: %v", err)
	}
	dropped := []Migration{{Version: "1.0.5", Note: "x", Ask: []string{"old"}}, {Version: "1.1.0", Note: "y", Ask: []string{"pat"}}}
	if err := CheckMigrations("1.0.0", "1.1.0", dropped, before, after); err != nil {
		t.Errorf("a setting a later version dropped shouldn't block: %v", err)
	}
	renamed := []SettingStatus{{Key: "address", Required: true}}
	if err := CheckMigrations("1.0.0", "1.1.0", []Migration{{Version: "1.1.0", Note: "x", Rename: map[string]string{"address": "host"}}}, before[:0], append(renamed[:0:0], SettingStatus{Key: "host", Required: true})); err != nil {
		t.Errorf("a rename carries a value: %v", err)
	}
	withDefault := []SettingStatus{{Key: "mode", Required: true, Default: "a"}}
	if err := CheckMigrations("1.0.0", "1.1.0", nil, nil, withDefault); err != nil {
		t.Errorf("a default needs no prompt: %v", err)
	}
}
