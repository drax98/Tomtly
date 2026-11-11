package extension

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const (
	upgradeSecret = "hunter2-the-secret"
	hostAndToken  = `host:{label:"Host",type:"string",required:true},token:{label:"Token",type:"secret",required:true}`
	addressAndKey = `address:{label:"Address",type:"string",required:true},token:{label:"Token",type:"secret",required:true},gone:{label:"Gone",type:"string"}`
)

// upgradeCode is a version of one extension, whose data says which version runs and what shown says about its settings.
func upgradeCode(version, settings, shown string) string {
	return `defineExtension({id:"upg",name:"Upg",version:"` + version + `",settings:{` + settings + `},data:{fetch(c){return {v:"` + version + `",seen:` + shown + `}}}});`
}

// installedOne runs version 1.0.0 from a folder, with address, token and gone set.
func installedOne(t *testing.T) *Manager {
	t.Helper()
	m := pageManager(t)
	write(t, m.configDir, "extensions/one/"+entryFile, upgradeCode("1.0.0", addressAndKey, `c.settings.address`))
	if _, err := addSeen(m, context.Background(), "./extensions/one", map[string]any{"address": "1.2.3.4", "token": upgradeSecret, "gone": "x"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "version 1.0.0", func() bool { return status(m).Running })
	return m
}

// nextVersion puts a version in a folder, with its migrations by version.
func nextVersion(t *testing.T, m *Manager, settings, shown string, migrations map[string]string) string {
	t.Helper()
	write(t, m.configDir, "extensions/two/"+entryFile, upgradeCode("1.2.0", settings, shown))
	for version, text := range migrations {
		write(t, m.configDir, "extensions/two/migrations/"+version+".json", text)
	}
	return "./extensions/two"
}

func keysAsked(plan *Upgrade) string {
	var keys []string
	for _, s := range plan.Ask {
		keys = append(keys, s.Key)
	}
	return strings.Join(keys, ",")
}

func TestReplaceWithoutMigrationsCarriesWhatIsStillDeclared(t *testing.T) {
	m := installedOne(t)
	two := nextVersion(t, m, `address:{label:"Address",type:"string",required:true},token:{label:"Token",type:"secret",required:true}`, `c.settings.address`, nil)
	found, err := m.Inspect(context.Background(), two)
	if err != nil || found.Upgrade == nil || len(found.Upgrade.Steps) != 0 || len(found.Upgrade.Ask) != 0 {
		t.Fatalf("%v, %+v", err, found.Upgrade)
	}
	if _, err := replaceSeen(m, context.Background(), "./extensions/one", two, nil); err != nil {
		t.Fatal(err)
	}
	saved := savedSettings(t, m, two)
	if saved["address"] != "1.2.3.4" || saved["token"] != upgradeSecret || len(saved) != 2 {
		t.Errorf("saved %v", saved)
	}
	eventually(t, "the new version", func() bool { return status(m).Version == "1.2.0" && status(m).Running })
}

func TestInspectShowsThePlanOnlyForAnUpdate(t *testing.T) {
	m := installedOne(t)
	two := nextVersion(t, m, hostAndToken+`,pat:{label:"Personal access token",type:"secret",required:true,help:"From the settings page"}`, `c.settings.host`, map[string]string{
		"1.1.0": `{"version":"1.1.0","note":"The address is now the host","rename":{"address":"host"}}`,
		"1.2.0": `{"version":"1.2.0","note":"Needs a token","ask":["pat","token"]}`,
	})
	found, err := m.Inspect(context.Background(), two)
	if err != nil {
		t.Fatal(err)
	}
	plan := found.Upgrade
	if plan == nil || plan.From != "1.0.0" || plan.To != "1.2.0" || len(plan.Steps) != 2 || plan.Steps[0].Note != "The address is now the host" {
		t.Fatalf("%+v", plan)
	}
	if keysAsked(plan) != "pat,token" || plan.Ask[0].Label != "Personal access token" || plan.Ask[0].Help == "" || plan.Ask[0].Set || !plan.Ask[1].Set {
		t.Errorf("%+v", plan.Ask)
	}
	if data, _ := json.Marshal(found); strings.Contains(string(data), upgradeSecret) {
		t.Errorf("a secret was shown: %s", data)
	}

	if same, err := m.Inspect(context.Background(), "./extensions/one"); err != nil || same.Upgrade != nil {
		t.Errorf("what already runs isn't an update: %v, %+v", err, same.Upgrade)
	}
	if other, err := m.Inspect(context.Background(), "./extensions/mine"); err != nil || other.Upgrade != nil {
		t.Errorf("another extension isn't an update: %v, %+v", err, other.Upgrade)
	}
	data, _ := json.Marshal(status(m))
	if strings.Contains(string(data), `"upgrade"`) {
		t.Errorf("a status has no plan: %s", data)
	}
}

func TestReplaceAppliesAnswersAndRenames(t *testing.T) {
	m := installedOne(t)
	two := nextVersion(t, m, hostAndToken+`,pat:{label:"PAT",type:"secret",required:true}`, `c.settings.host+"/"+c.settings.pat.length+"/"+c.settings.token.length`, map[string]string{
		"1.1.0": `{"version":"1.1.0","note":"renamed","rename":{"address":"host"}}`,
		"1.2.0": `{"version":"1.2.0","note":"needs a pat","ask":["pat","token"]}`,
	})
	got, err := replaceSeen(m, context.Background(), "./extensions/one", two, map[string]any{"pat": "12345", "token": ""}) // the blank token stays
	if err != nil {
		t.Fatal(err)
	}
	saved := savedSettings(t, m, two)
	if saved["host"] != "1.2.3.4" || saved["pat"] != "12345" || saved["token"] != upgradeSecret || saved["address"] != nil {
		t.Errorf("saved %v", saved)
	}
	eventually(t, "it to restart with them", func() bool {
		d, _, _ := m.plugins.Get("upg")
		return strings.Contains(string(d), `"seen":"1.2.3.4/5/18"`)
	})
	if data, _ := json.Marshal(got); strings.Contains(string(data), upgradeSecret) || strings.Contains(string(data), "12345") {
		t.Errorf("a secret was shown: %s", data)
	}
}

func TestReplaceRefusalsChangeNothing(t *testing.T) {
	asks := map[string]string{"1.2.0": `{"version":"1.2.0","note":"needs a pat","ask":["pat"]}`}
	settings := addressAndKey + `,pat:{label:"Personal access token",type:"secret",required:true}`
	for name, c := range map[string]struct {
		given map[string]any
		want  string
	}{
		"a missing required setting":      {nil, `"pat"`},
		"a blank required setting":        {map[string]any{"pat": ""}, "Personal access token"},
		"a setting the update didn't ask": {map[string]any{"pat": "x", "token": "stolen"}, `doesn't ask for the setting "token"`},
		"the wrong type":                  {map[string]any{"pat": 5.0}, "must be a secret"},
	} {
		t.Run(name, func(t *testing.T) {
			m := installedOne(t)
			two := nextVersion(t, m, settings, `c.settings.address`, asks)
			before := m.bySource("./extensions/one")
			_, err := replaceSeen(m, context.Background(), "./extensions/one", two, c.given)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v", err)
			}
			if m.bySource("./extensions/one") != before || !status(m).Running || status(m).Version != "1.0.0" {
				t.Errorf("the old version must go on: %+v", status(m))
			}
			added, _ := m.store.AddedExtensions(context.Background())
			if len(added) != 1 || added[0].Source != "./extensions/one" || added[0].Settings["token"] != upgradeSecret {
				t.Errorf("the store changed: %+v", added)
			}
		})
	}
}

// The dialog stays open after a refusal, so pressing Update again has to find the download still there.
func TestARefusedUploadCanBeRetried(t *testing.T) {
	m := installedOne(t)
	ctx := context.Background()
	up, err := m.Upload(ctx, "x.zip", zipOf(t, map[string]string{
		entryFile:               upgradeCode("1.2.0", addressAndKey+`,pat:{label:"PAT",type:"secret",required:true}`, `c.settings.pat.length`),
		"migrations/1.2.0.json": `{"version":"1.2.0","note":"needs a pat","ask":["pat"]}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if up.Upgrade == nil || keysAsked(up.Upgrade) != "pat" {
		t.Fatalf("%+v", up.Upgrade)
	}
	if _, err := replaceSeen(m, ctx, "./extensions/one", up.Source, nil); err == nil {
		t.Fatal("it needed a value")
	}
	if !stored(t, m, up.Source) {
		t.Fatal("the download was thrown away")
	}
	if _, err := replaceSeen(m, ctx, "./extensions/one", up.Source, map[string]any{"pat": "123"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { d, _, _ := m.plugins.Get("upg"); return strings.Contains(string(d), `"seen":3`) })
}

func TestRemoveAndAskForcesANewValue(t *testing.T) {
	m := installedOne(t)
	two := nextVersion(t, m, addressAndKey, `c.settings.token.length`, map[string]string{
		"1.2.0": `{"version":"1.2.0","note":"Old tokens stopped working","remove":["token"],"ask":["token"]}`,
	})
	ctx := context.Background()
	found, err := m.Inspect(ctx, two)
	if err != nil || keysAsked(found.Upgrade) != "token" || found.Upgrade.Ask[0].Set {
		t.Fatalf("%v, %+v", err, found.Upgrade)
	}
	if _, err := replaceSeen(m, ctx, "./extensions/one", two, map[string]any{"token": ""}); err == nil || !strings.Contains(err.Error(), `"token"`) {
		t.Fatalf("a blank token was kept: %v", err)
	}
	if _, err := replaceSeen(m, ctx, "./extensions/one", two, map[string]any{"token": "new"}); err != nil {
		t.Fatal(err)
	}
	if saved := savedSettings(t, m, two); saved["token"] != "new" {
		t.Errorf("saved %v", saved)
	}
}

func TestABadMigrationFileRefusesTheUpdate(t *testing.T) {
	m := installedOne(t)
	ctx := context.Background()
	bad := zipOf(t, map[string]string{entryFile: upgradeCode("1.2.0", addressAndKey, `1`), "migrations/1.2.0.json": `{"version":"1.2.0"}`})
	if _, err := m.Upload(ctx, "x.zip", bad); err == nil || !strings.Contains(err.Error(), "needs a note") {
		t.Fatalf("got %v", err)
	}
	two := nextVersion(t, m, addressAndKey, `1`, map[string]string{"1.2.0": `{"version":"1.2.0","note":"x","asks":["token"]}`})
	if _, err := m.Inspect(ctx, two); err == nil || !strings.Contains(err.Error(), "migration file") {
		t.Fatalf("inspect: %v", err)
	}
	if _, err := replaceSeen(m, ctx, "./extensions/one", two, nil); err == nil || !strings.Contains(err.Error(), "migration file") {
		t.Fatalf("replace: %v", err)
	}
	if status(m).Version != "1.0.0" {
		t.Error("the old version must go on")
	}
	// Installing it fresh goes through no migrations, so their files don't matter.
	m.Remove("./extensions/one")
	if _, err := m.Inspect(ctx, two); err != nil {
		t.Errorf("fresh: %v", err)
	}
}

func TestAVersionCallingASecretPlainTextDoesNotGetToShowIt(t *testing.T) {
	m := installedOne(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		settings   string
		migrations map[string]string
		asked      string
	}{
		"same key": {`address:{label:"Address",type:"string",required:true},token:{label:"Token",type:"string",required:true}`,
			map[string]string{"1.2.0": `{"version":"1.2.0","note":"x","ask":["token"]}`}, "token"},
		"renamed": {`address:{label:"Address",type:"string",required:true},shown:{label:"Shown",type:"string",required:true}`,
			map[string]string{"1.2.0": `{"version":"1.2.0","note":"x","rename":{"token":"shown"},"ask":["shown"]}`}, "shown"},
	} {
		t.Run(name, func(t *testing.T) {
			two := nextVersion(t, m, c.settings, `1`, c.migrations)
			found, err := m.Inspect(ctx, two)
			if err != nil {
				t.Fatal(err)
			}
			if data, _ := json.Marshal(found); strings.Contains(string(data), upgradeSecret) {
				t.Fatalf("a secret was shown: %s", data)
			}
			if keysAsked(found.Upgrade) != c.asked || found.Upgrade.Ask[0].Set {
				t.Errorf("it should be asked for again: %+v", found.Upgrade.Ask)
			}
		})
	}
}

func TestMarketUpgradeStepsAndRepository(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	first, _ := m.MarketInspect(ctx, "weather")
	if _, err := addSeen(m, ctx, first.Source, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to load", func() bool { return m.Statuses()[0].ID != "" })

	code := strings.Replace(strings.Replace(marketCode, `"1.2.0"`, `"1.4.0"`, 1), `data:{`, `settings:{pat:{label:"Personal access token",type:"secret",required:true}},data:{`, 1)
	f.extra = map[string]string{"migrations/1.3.0.json": `{"version":"1.3.0","note":"Needs a token","ask":["pat"]}`}
	f.addAs("weather", "1.4.0", code, "Internet: api.example.com")
	f.entries[0].Migrations = []Migration{
		{Version: "1.2.0", Note: "already there", Ask: []string{"old"}},
		{Version: "1.3.0", Note: "Needs a token", Ask: []string{"pat"}},
		{Version: "1.5.0", Note: "not yet", Ask: []string{"later"}},
	}
	listed := m.Marketplace(ctx).Entries[0]
	if !listed.UpdateAvailable || len(listed.UpgradeSteps) != 1 || listed.UpgradeSteps[0].Version != "1.3.0" || listed.UpgradeSteps[0].Ask[0] != "pat" {
		t.Fatalf("%+v", listed)
	}
	if listed.Repository != "https://example.com/a/weather" {
		t.Errorf("%+v", listed)
	}
	found, err := m.MarketInspect(ctx, "weather")
	if err != nil || found.Upgrade == nil || keysAsked(found.Upgrade) != "pat" || found.Upgrade.From != "1.2.0" {
		t.Fatalf("%v, %+v", err, found.Upgrade)
	}
	if _, err := replaceSeen(m, ctx, first.Source, found.Source, nil); err == nil || !strings.Contains(err.Error(), "Personal access token") {
		t.Fatalf("got %v", err)
	}
	if _, err := replaceSeen(m, ctx, first.Source, found.Source, map[string]any{"pat": "abc"}); err != nil {
		t.Fatal(err)
	}
	if steps := m.Marketplace(ctx).Entries[0].UpgradeSteps; len(steps) != 0 {
		t.Errorf("nothing is left to go through: %+v", steps)
	}
}

// The link is the author's own and nothing depends on it, but a page mustn't be handed something that isn't a web address.
func TestAnEntrysLinkIsOptionalAndOnlyAWebAddress(t *testing.T) {
	for repository, listed := range map[string]bool{"": true, "https://gitlab.com/a/b": true, "http://example.com/a": true, "javascript:alert(1)": false, "github.com/a/b": false} {
		f := newFakeMarket(t)
		f.entries[0].Repository = repository
		if v := marketManager(t, f).Marketplace(context.Background()); (len(v.Entries) == 1) != listed || !v.Verified {
			t.Errorf("%q: %+v", repository, v)
		}
	}
}
