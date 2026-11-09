package extension

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"home-control/internal/store"
)

const hostToken = "tok3n-of-the-owner"

func counted(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://"), hits
}

func needApproval(t *testing.T) {
	t.Helper()
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
}

// hostCode is a version of an extension that sends its token to the address in the setting named by name.
func hostCode(version, name, extra string) string {
	return `defineExtension({id:"sec",name:"Sec",version:"` + version + `",access:{network:["{` + name + `}"]},
		settings:{` + name + `:{label:"Address",type:"string",required:true},token:{label:"Token",type:"secret",required:true,sendTo:["{` + name + `}"]}` + extra + `},
		data:{everySeconds:3600,fetch(c){c.fetch("http://"+c.settings.` + name + `+"/?t="+c.settings.token);return {v:"` + version + `"}}}});`
}

// installedHost is version 1.0.0 added on the page, running against host.
func installedHost(t *testing.T, host string, extra string, settings map[string]any) *Manager {
	t.Helper()
	m := pageManager(t)
	m.allowLoopback = true
	write(t, m.configDir, "extensions/one/"+entryFile, hostCode("1.0.0", "host", extra))
	given := map[string]any{"host": host, "token": hostToken}
	for k, v := range settings {
		given[k] = v
	}
	if _, err := addSeen(m, context.Background(), "./extensions/one", given); err != nil {
		t.Fatal(err)
	}
	eventually(t, "version 1.0.0", func() bool { return status(m).Running })
	return m
}

func secondVersion(t *testing.T, m *Manager, code string, migrations map[string]string) string {
	t.Helper()
	write(t, m.configDir, "extensions/two/"+entryFile, code)
	for version, text := range migrations {
		write(t, m.configDir, "extensions/two/migrations/"+version+".json", text)
	}
	return "./extensions/two"
}

func TestAHostSettingCantHaveADefault(t *testing.T) {
	for name, source := range map[string]string{
		"network": `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"]},settings:{host:{label:"H",type:"string",default:"evil.example"}},data:{fetch(){return 1}}});`,
		"sendTo":  `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"]},settings:{host:{label:"H",type:"string",default:"evil.example"},k:{label:"K",type:"secret",sendTo:["{host}"]}},data:{fetch(){return 1}}});`,
	} {
		t.Run(name, func(t *testing.T) { failsWith(t, run(t, folder(t, source), nil), "can't have a default") })
	}
}

// The reported exploit: an update points {host} at the author's server with a default, and forgets the saved address.
func TestAnUpdateCantRedirectATokenWithADefault(t *testing.T) {
	needApproval(t)
	good, _ := counted(t)
	evil, evilHits := counted(t)
	m := installedHost(t, good, "", nil)
	code := strings.Replace(hostCode("1.1.0", "host", ""), `host:{label:"Address",type:"string",required:true}`, `host:{label:"Address",type:"string",default:["`+strings.Replace(evil, ":", `",":","`, 1)+`"].join("")}`, 1)
	two := secondVersion(t, m, code, map[string]string{"1.1.0": `{"version":"1.1.0","note":"x","remove":["host"]}`})
	if _, err := replaceSeen(m, context.Background(), "./extensions/one", two, nil); err == nil || !strings.Contains(err.Error(), "can't have a default") {
		t.Errorf("got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if evilHits.Load() != 0 {
		t.Fatal("the token went to the author")
	}
}

func TestAnUpdateKeepingTheAddressAsksNothing(t *testing.T) {
	needApproval(t)
	good, _ := counted(t)
	m := installedHost(t, good, "", nil)
	two := secondVersion(t, m, hostCode("1.1.0", "host", ""), nil)
	if _, err := replaceSeen(m, context.Background(), "./extensions/one", two, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new version", func() bool { s := status(m); return s.Version == "1.1.0" && s.Running && !s.NeedsApproval })
}

func waitsWithoutSending(t *testing.T, m *Manager, hits *atomic.Int32, address string) {
	t.Helper()
	eventually(t, "it to wait for approval", func() bool { return status(m).NeedsApproval })
	time.Sleep(50 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatal("it reached the new address without approval")
	}
	if got := strings.Join(status(m).Access, "|"); !strings.Contains(got, address) {
		t.Errorf("the page must show the address being approved: %s", got)
	}
}

func TestARenameIntoAHostSettingNeedsApproval(t *testing.T) {
	needApproval(t)
	good, _ := counted(t)
	evil, evilHits := counted(t)
	m := installedHost(t, good, `,note:{label:"Note",type:"string"}`, map[string]any{"note": evil})
	two := secondVersion(t, m, hostCode("1.1.0", "host", ""), map[string]string{"1.1.0": `{"version":"1.1.0","note":"x","rename":{"note":"host"}}`})
	if _, err := replaceSeen(m, context.Background(), "./extensions/one", two, nil); err != nil {
		t.Fatal(err)
	}
	waitsWithoutSending(t, m, evilHits, evil)
}

func TestAnAnswerTypedIntoAnUpdateNeedsApproval(t *testing.T) {
	needApproval(t)
	good, _ := counted(t)
	evil, evilHits := counted(t)
	m := installedHost(t, good, "", nil)
	two := secondVersion(t, m, hostCode("1.1.0", "host", ""), map[string]string{"1.1.0": `{"version":"1.1.0","note":"Type this address","remove":["host"],"ask":["host"]}`})
	if _, err := replaceSeen(m, context.Background(), "./extensions/one", two, map[string]any{"host": evil}); err != nil {
		t.Fatal(err)
	}
	waitsWithoutSending(t, m, evilHits, evil)
}

// The same address under another name could swap which address a sendTo secret goes to, so it is asked about once.
func TestRenamingTheHostSettingAsksOnce(t *testing.T) {
	needApproval(t)
	good, hits := counted(t)
	m := installedHost(t, good, "", nil)
	eventually(t, "version 1.0.0 to send", func() bool { return hits.Load() > 0 })
	before := hits.Load()
	two := secondVersion(t, m, hostCode("1.1.0", "address", ""), map[string]string{"1.1.0": `{"version":"1.1.0","note":"x","rename":{"host":"address"}}`})
	if _, err := replaceSeen(m, context.Background(), "./extensions/one", two, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
	if hits.Load() != before {
		t.Fatal("it ran before approval")
	}
	if _, err := approveSeen(m, two); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { return status(m).Running && hits.Load() > before })
}

func TestChangingTheHostOnThePageIsTheApproval(t *testing.T) {
	needApproval(t)
	first, _ := counted(t)
	second, secondHits := counted(t)
	m := installedHost(t, first, "", nil)
	if _, err := m.UpdateSettings(context.Background(), "./extensions/one", map[string]any{"host": second}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new address", func() bool { return secondHits.Load() > 0 })
	if s := status(m); s.NeedsApproval || !s.Running {
		t.Fatalf("it asked twice: %+v", s)
	}
	grants, _ := m.store.Grants(context.Background(), "sec")
	if got := strings.Join(grants, "|"); got != "secret:token|send:token:{host}|network:{host}="+second {
		t.Errorf("grants are %s", got)
	}
}

func TestEditingSettingsDoesNotApproveAnythingElse(t *testing.T) {
	needApproval(t)
	first, _ := counted(t)
	second, secondHits := counted(t)
	m := installedHost(t, first, `,note:{label:"Note",type:"string"}`, nil)
	ctx := context.Background()
	m.store.SetGrants(ctx, "sec", nil) // as being stopped for its violations does
	if _, err := m.UpdateSettings(ctx, "./extensions/one", map[string]any{"note": "x"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
	if _, err := m.UpdateSettings(ctx, "./extensions/one", map[string]any{"host": second}); err != nil {
		t.Fatal(err)
	}
	waitsWithoutSending(t, m, secondHits, second)
	if grants, _ := m.store.Grants(ctx, "sec"); len(grants) != 0 {
		t.Errorf("grants are %v", grants)
	}
}

func TestAnApprovalMadeBeforeAddressesCountedAsksAgain(t *testing.T) {
	needApproval(t)
	address, hits := counted(t)
	m := run(t, folder(t, hostCode("1.0.0", "host", "")), map[string]any{"host": address, "token": hostToken}, allowLoopback, func(m *Manager) {
		m.store.SetGrants(context.Background(), "sec", []string{"network:{host}"})
	})
	waitsWithoutSending(t, m, hits, address)
	if _, err := approveSeen(m, status(m).Source); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { return hits.Load() > 0 })
}

func TestAnAddressChangedInConfigAsksAgain(t *testing.T) {
	needApproval(t)
	first, _ := counted(t)
	second, secondHits := counted(t)
	dir, shared := folder(t, hostCode("1.0.0", "host", "")), store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newManager(t.TempDir(), t.TempDir(), shared, hub(t))
	m.allowLoopback = true
	t.Cleanup(func() { stopAll(m) })
	m.add(ctx, entry{Source: dir, Settings: map[string]any{"host": first, "token": hostToken}}, false)
	eventually(t, "it to wait", func() bool { return status(m).NeedsApproval })
	if _, err := approveSeen(m, dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { return status(m).Running })
	stopAll(m)
	again := newManager(t.TempDir(), t.TempDir(), shared, hub(t))
	again.allowLoopback = true
	t.Cleanup(func() { stopAll(again) })
	again.add(context.Background(), entry{Source: dir, Settings: map[string]any{"host": second, "token": hostToken}}, false)
	waitsWithoutSending(t, again, secondHits, second)
}

func TestAccessLabelsShowTheAddress(t *testing.T) {
	address, _ := counted(t)
	m := installedHost(t, address, "", nil)
	ctx := context.Background()
	if got := strings.Join(status(m).Access, "|"); !strings.Contains(got, address) {
		t.Errorf("running: %s", got)
	}
	// An update keeping the address must not look like it asks for more, as the update dialog compares these.
	two := secondVersion(t, m, hostCode("1.1.0", "host", ""), nil)
	found, err := m.Inspect(ctx, two)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(found.Access, status(m).Access) {
		t.Errorf("update %v, running %v", found.Access, status(m).Access)
	}
	// With no address yet, the words stay as they are, which is what signed marketplace listings hold.
	m.Remove("./extensions/one")
	fresh, err := m.Inspect(ctx, two)
	if err != nil || fresh.Access[0] != "Local network: the address in the setting host" {
		t.Errorf("%v, %v", err, fresh.Access)
	}
}

func TestBundledHostExtensionsStillLoad(t *testing.T) {
	for _, name := range []string{"gree", "hue", "service-bridge", "http-json"} {
		if _, err := load(example(t, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
