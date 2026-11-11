package extension

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Migration is one file in an extension's migrations folder, migrations/<version>.json. It says what someone updating
// to that version has to do. The new setting itself, with its label and help, is declared in the code as usual.
type Migration struct {
	Version string            `json:"version"`
	Note    string            `json:"note"`
	Ask     []string          `json:"ask,omitempty"`    // settings to prompt for; a value already there is kept if left blank
	Rename  map[string]string `json:"rename,omitempty"` // old key to new key, so a saved value carries over
	Remove  []string          `json:"remove,omitempty"` // saved values to forget. Also in ask, this forces a new value, as for a token that stopped working
}

// UpgradeStep is what one version says to the person updating.
type UpgradeStep struct {
	Version string `json:"version"`
	Note    string `json:"note"`
}

// Upgrade is everything that happens to settings between two versions. Applying it never touches the extension's data.
type Upgrade struct {
	From  string          `json:"from"`
	To    string          `json:"to"`
	Steps []UpgradeStep   `json:"steps"`
	Ask   []SettingStatus `json:"ask"` // what to prompt for, each with Set saying whether a value carries over
}

// ReadMigrations reads a folder's migrations, oldest first. A folder without any is fine, but a bad file is an error.
func ReadMigrations(dir string) ([]Migration, error) {
	files, err := filepath.Glob(filepath.Join(dir, "migrations", "*"))
	if err != nil {
		return nil, err
	}
	out := []Migration{}
	for _, file := range files {
		name := filepath.Base(file)
		version, ok := strings.CutSuffix(name, ".json")
		if !ok {
			return nil, fmt.Errorf("migrations/%s: a migration is a file named <version>.json", name)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("migrations/%s: %w", name, err)
		}
		var m Migration
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("migrations/%s: %w", name, err)
		}
		if m.Version != version {
			return nil, fmt.Errorf("migrations/%s: it says its version is %q", name, m.Version)
		}
		if _, ok := parseVersion(version); !ok {
			return nil, fmt.Errorf("migrations/%s: %q isn't a version like 1.2.0", name, version)
		}
		if strings.TrimSpace(m.Note) == "" {
			return nil, fmt.Errorf("migrations/%s: needs a note saying what changed", name)
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Migration) int {
		if newerVersion(a.Version, b.Version) {
			return 1
		}
		return -1
	})
	return out, nil
}

// PlanUpgrade works out what moving between versions needs. saved holds the current settings, by key, and declared is
// the new version's settings. It returns the settings to carry over, with renames and removals applied.
func PlanUpgrade(from, to string, migrations []Migration, declared []SettingStatus, saved map[string]any) (Upgrade, map[string]any) {
	plan := Upgrade{From: from, To: to, Steps: []UpgradeStep{}, Ask: []SettingStatus{}}
	settings := make(map[string]any, len(saved))
	for key, value := range saved {
		settings[key] = value
	}
	asked := map[string]bool{}
	for _, m := range migrations {
		if !newerVersion(m.Version, from) || newerVersion(m.Version, to) {
			continue
		}
		plan.Steps = append(plan.Steps, UpgradeStep{m.Version, m.Note})
		for old, renamed := range m.Rename {
			if value, ok := settings[old]; ok {
				settings[renamed] = value
				delete(settings, old)
			}
		}
		for _, key := range m.Remove {
			delete(settings, key)
		}
		for _, key := range m.Ask {
			asked[key] = true
		}
	}
	names := map[string]bool{}
	for _, s := range declared {
		names[s.Key] = true
	}
	for key := range settings {
		if !names[key] {
			delete(settings, key) // the new version doesn't have it, and resolveSettings refuses unknown ones
		}
	}
	for _, s := range declared {
		_, has := settings[s.Key]
		// A required setting nothing asked for is still needed, or the extension couldn't start.
		if !asked[s.Key] && (has || !s.Required || s.Default != nil) {
			continue
		}
		s.Set, s.Value = has, nil
		if has && s.Type != "secret" {
			s.Value = settings[s.Key]
		}
		plan.Ask = append(plan.Ask, s)
	}
	return plan, settings
}

// CheckMigrations refuses a new version that would surprise people updating to it: a required setting it adds has to be
// asked for by a migration between the two versions, and every migration has to name settings that exist.
func CheckMigrations(from, to string, migrations []Migration, before, after []SettingStatus) error {
	has := func(list []SettingStatus, key string) bool {
		return slices.ContainsFunc(list, func(s SettingStatus) bool { return s.Key == key })
	}
	asked := map[string]bool{}
	for _, m := range migrations {
		if !newerVersion(m.Version, from) || newerVersion(m.Version, to) {
			continue
		}
		for _, key := range m.Ask {
			// An older migration may ask for something a later version dropped, and PlanUpgrade skips it.
			if m.Version == to && !has(after, key) {
				return fmt.Errorf("migrations/%s.json asks for %q, which version %s doesn't declare", m.Version, key, to)
			}
			asked[key] = true
		}
		for old := range m.Rename {
			asked[m.Rename[old]] = true
		}
	}
	for _, s := range after {
		if s.Required && s.Default == nil && !has(before, s.Key) && !asked[s.Key] {
			return fmt.Errorf("version %s adds the required setting %q, so it needs a migration between %s and %s that asks for it", to, s.Key, from, to)
		}
	}
	return nil
}

// migrationsBetween picks the migrations a move between two versions goes through, as PlanUpgrade does.
func migrationsBetween(migrations []Migration, from, to string) []Migration {
	var out []Migration
	for _, m := range migrations {
		if newerVersion(m.Version, from) && !newerVersion(m.Version, to) {
			out = append(out, m)
		}
	}
	return out
}

// installedAs finds the extension added on the page that a version from source would replace.
//
// shortcut: a config.json extension is left out, since its settings are read-only on the page and its card already names
// a missing setting; include it if config.json settings ever become editable there.
func (m *Manager) installedAs(id, source string) *instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.instances {
		if got, _ := inst.identity(); got == id && inst.removable && inst.entry.Source != source {
			return inst
		}
	}
	return nil
}

// planFor works out moving a running extension to a new version in dir: what to ask, and the settings to carry over.
func planFor(inst *instance, l *loaded, dir string) (Upgrade, map[string]any, error) {
	migrations, err := ReadMigrations(dir)
	if err != nil {
		return Upgrade{}, nil, fmt.Errorf("this version can't be updated to, because a migration file is wrong: %w", err)
	}
	running := inst.currentStatus()
	declared := definedStatus("", l, nil).Settings
	saved := maps.Clone(inst.entry.Settings)
	plan, carried := PlanUpgrade(running.Version, l.Version, migrations, declared, saved)
	// A version that calls an old secret plain text would have it shown on the page, so it's asked for again instead.
	again := false
	for _, s := range running.Settings {
		secret, isText := saved[s.Key].(string)
		if s.Type != "secret" || !isText {
			continue
		}
		if slices.ContainsFunc(declared, func(d SettingStatus) bool { return d.Type != "secret" && carried[d.Key] == secret }) {
			delete(saved, s.Key)
			again = true
		}
	}
	if again {
		plan, carried = PlanUpgrade(running.Version, l.Version, migrations, declared, saved)
	}
	return plan, carried, nil
}

// applyAnswers merges what the person typed over the carried settings, and refuses a result the new version can't start
// with. Nothing has changed yet when it refuses. A blank answer keeps a value that is already there.
func applyAnswers(plan Upgrade, declared map[string]Setting, settings, given map[string]any) error {
	for key, value := range given {
		if !slices.ContainsFunc(plan.Ask, func(s SettingStatus) bool { return s.Key == key }) {
			return fmt.Errorf("this update doesn't ask for the setting %q, so it can't be changed here", key)
		}
		if value != nil && value != "" {
			settings[key] = value
		}
	}
	_, err := resolveSettings(declared, settings)
	return err
}
