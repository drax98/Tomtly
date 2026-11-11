package extension

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAHomeWithOnlyTheOldSettingRefusesAndSaysWhatToSet(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := pageManager(t)
	if err := m.SetMarketplace(f.url, f.pub, ""); err != nil {
		t.Fatal(err)
	}
	v := m.Marketplace(ctx)
	if !v.Configured || v.Verified || len(v.Entries) != 0 || !strings.Contains(v.Error, "MARKETPLACE_APPROVAL_PUBLIC_KEYS") {
		t.Fatalf("%+v", v)
	}
	if _, err := m.MarketInspect(ctx, "weather"); err == nil || !strings.Contains(err.Error(), "MARKETPLACE_APPROVAL_PUBLIC_KEYS") {
		t.Errorf("inspect: %v", err)
	}
}

func TestOneKeyCantBeBoth(t *testing.T) {
	f := newFakeMarket(t)
	m := pageManager(t)
	if err := m.SetMarketplace(f.url, f.pub, f.pub); err != nil {
		t.Fatal(err)
	}
	if v := m.Marketplace(context.Background()); v.Verified || !strings.Contains(v.Error, "different") {
		t.Fatalf("%+v", v)
	}
}

func TestAnEntryNeedsTheApprovalKeysSignature(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	f.unsigned = true
	m := marketManager(t, f)
	v := m.Marketplace(ctx)
	if !v.Verified || len(v.Entries) != 0 {
		t.Fatalf("an entry only the web key vouches for was listed: %+v", v)
	}
	if _, err := m.MarketInspect(ctx, "weather"); err == nil {
		t.Error("it could be downloaded")
	}
}

func TestEitherKeySettingTakesAList(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	other, _, _ := ed25519.GenerateKey(nil)
	another, _, _ := ed25519.GenerateKey(nil)
	m := pageManager(t)
	if err := m.SetMarketplace(f.url, base64.StdEncoding.EncodeToString(other)+", "+f.pub, testApprovalPub+","+base64.StdEncoding.EncodeToString(another)); err != nil {
		t.Fatal(err)
	}
	if v := m.Marketplace(ctx); !v.Verified || len(v.Entries) != 1 {
		t.Fatalf("%+v", v)
	}
	if err := pageManager(t).SetMarketplace(f.url, f.pub, "nope"); err == nil {
		t.Error("a bad approval key was accepted")
	}
}

// A definition can answer differently each time it runs, so a home grants only what the person saw.
func TestAddTakesWhatWasShown(t *testing.T) {
	ctx := context.Background()
	m := pageManager(t)
	write(t, m.configDir, "extensions/one/"+entryFile, `defineExtension({id:"one",name:"One",version:"1.0.0",access:{network:["a.example.com"]},data:{fetch(){return 1}}});`)
	shown, err := m.Inspect(ctx, "./extensions/one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, "./extensions/one", nil, nil); err == nil || !strings.Contains(err.Error(), "shown") {
		t.Errorf("nothing shown: %v", err)
	}
	write(t, m.configDir, "extensions/one/"+entryFile, `defineExtension({id:"one",name:"One",version:"1.0.0",access:{network:["evil.example.com"]},data:{fetch(){return 1}}});`)
	if _, err := m.Add(ctx, "./extensions/one", nil, shown.Access); err == nil || !strings.Contains(err.Error(), "shown") {
		t.Errorf("it asks for something else now: %v", err)
	}
	if grants, _ := m.store.Grants(ctx, "one"); len(grants) != 0 || len(m.Statuses()) != 0 {
		t.Errorf("added anyway: %v", grants)
	}
	again, _ := m.Inspect(ctx, "./extensions/one")
	if _, err := m.Add(ctx, "./extensions/one", nil, again.Access); err != nil {
		t.Errorf("after looking again: %v", err)
	}
}

func TestApproveTakesWhatWasShown(t *testing.T) {
	needApproval(t)
	m := run(t, folder(t, `defineExtension({id:"one",name:"One",version:"1.0.0",access:{network:["a.example.com"]},data:{fetch(){return 1}}});`), nil)
	eventually(t, "it to wait", func() bool { return m.Statuses()[0].NeedsApproval })
	source := m.Statuses()[0].Source
	if _, err := m.Approve(source, nil); err == nil || !strings.Contains(err.Error(), "shown") {
		t.Errorf("nothing shown: %v", err)
	}
	if _, err := m.Approve(source, []string{"Internet: other.example.com"}); err == nil {
		t.Error("something else shown")
	}
	if grants, _ := m.store.Grants(context.Background(), "one"); len(grants) != 0 {
		t.Errorf("granted: %v", grants)
	}
	if _, err := m.Approve(source, m.Statuses()[0].Access); err != nil {
		t.Error(err)
	}
}

func TestReplaceTakesWhatWasShown(t *testing.T) {
	ctx := context.Background()
	m := pageManager(t)
	write(t, m.configDir, "extensions/v1/"+entryFile, `defineExtension({id:"one",name:"One",version:"1.0.0",data:{fetch(){return 1}}});`)
	write(t, m.configDir, "extensions/v2/"+entryFile, `defineExtension({id:"one",name:"One",version:"2.0.0",access:{network:["a.example.com"]},data:{fetch(){return 1}}});`)
	if _, err := addSeen(m, ctx, "./extensions/v1", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to load", func() bool { return m.Statuses()[0].ID != "" })
	if _, err := m.Replace(ctx, "./extensions/v1", "./extensions/v2", nil, nil); err == nil || !strings.Contains(err.Error(), "shown") {
		t.Errorf("got %v", err)
	}
	if m.Statuses()[0].Source != "./extensions/v1" {
		t.Error("replaced anyway")
	}
	if _, err := replaceSeen(m, ctx, "./extensions/v1", "./extensions/v2", nil); err != nil {
		t.Error(err)
	}
}

func TestAddComparesWithTheSignedEntry(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	found, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.entries[0].Access = []string{"Internet: something.else"}
	f.mu.Unlock()
	if _, err := m.Add(ctx, found.Source, nil, found.Access); err == nil || !strings.Contains(err.Error(), "marketplace listed") {
		t.Errorf("got %v", err)
	}
}

func TestWhatLeftTheCatalogCantBeAdded(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	found, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.Package(ctx, "market-"+f.entries[0].SHA256); err != nil {
		t.Fatal("it should be stored by now")
	}
	check := func(name string, change func(), want string) {
		t.Helper()
		f.mu.Lock()
		entries, revoked := f.entries, f.revoked
		change()
		f.mu.Unlock()
		if _, err := m.Add(ctx, found.Source, nil, found.Access); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
		f.mu.Lock()
		f.entries, f.revoked = entries, revoked
		f.mu.Unlock()
	}
	check("withdrawn", func() { f.entries = nil }, "no longer offers")
	check("suspended", func() {
		f.revoked = []MarketRevocation{{ID: "weather", Version: "1.2.0", State: "suspended", Reason: "A report"}}
	}, "A report")
	check("not reachable", func() { f.down = true }, "can't be reached")
	f.down = false
	if _, err := m.Add(ctx, found.Source, nil, found.Access); err != nil {
		t.Errorf("on offer again: %v", err)
	}
}

func TestReplaceChecksTheCatalogAndNeverGoesBack(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	first, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(ctx, first.Source, nil, first.Access); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to load", func() bool { return m.Statuses()[0].ID != "" })

	// A package on offer, then taken away after it was downloaded.
	f.addAs("weather", "1.3.0", strings.Replace(marketCode, `"1.2.0"`, `"1.3.0"`, 1), "Internet: api.example.com")
	next, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.revoked = []MarketRevocation{{ID: "weather", Version: "1.3.0", State: "suspended", Reason: "A report"}}
	f.mu.Unlock()
	if _, err := m.Replace(ctx, first.Source, next.Source, nil, next.Access); err == nil || !strings.Contains(err.Error(), "A report") {
		t.Errorf("suspended: %v", err)
	}

	// An older version on offer, which is what someone holding a signed copy could try.
	f.mu.Lock()
	f.revoked = nil
	f.mu.Unlock()
	f.addAs("weather", "1.1.0", strings.Replace(marketCode, `"1.2.0"`, `"1.1.0"`, 1), "Internet: api.example.com")
	older, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Replace(ctx, first.Source, older.Source, nil, older.Access); err == nil || !strings.Contains(err.Error(), "older") {
		t.Errorf("a downgrade: %v", err)
	}
	if m.Statuses()[0].Source != first.Source {
		t.Error("replaced anyway")
	}
}

func TestAnOldListIsRefusedAcrossARestart(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	if !m.Marketplace(ctx).Verified {
		t.Fatal("first catalog refused")
	}
	again := newManager(t.TempDir(), t.TempDir(), m.store, hub(t))
	if err := again.SetMarketplace(f.url, f.pub, testApprovalPub); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.at = f.at.Add(-time.Hour)
	f.expires = time.Now().Add(time.Hour)
	f.mu.Unlock()
	if v := again.Marketplace(ctx); v.Verified || !strings.Contains(v.Error, "older") {
		t.Fatalf("%+v", v)
	}
}

func TestAnExpiredListIsRefusedAndTheHomeSaysSo(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	f.at = time.Now().Add(-8 * 24 * time.Hour) // held back since then, with nothing wrong with its signature
	f.expires = f.at.Add(7 * 24 * time.Hour)
	m := marketManager(t, f)
	v := m.Marketplace(ctx)
	if v.Verified || len(v.Entries) != 0 || !strings.Contains(v.Error, "expired") {
		t.Fatalf("%+v", v)
	}
	if _, err := m.MarketInspect(ctx, "weather"); err == nil {
		t.Error("it could be downloaded")
	}
}

func TestAListWithoutFreshnessIsRefused(t *testing.T) {
	h := newHostileMarket(t, nil)
	h.bare = true
	if v := hostileManager(t, h).Marketplace(context.Background()); v.Verified {
		t.Fatalf("%+v", v)
	}
}

func installed(t *testing.T, m *Manager) Status {
	t.Helper()
	found, err := m.MarketInspect(context.Background(), "weather")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), found.Source, nil, found.Access); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run", func() bool { s := m.Statuses(); return len(s) == 1 && s[0].Running })
	return m.Statuses()[0]
}

// A home hears on its own, keeps running, stops only when told, and the news survives a restart with the marketplace down.
func TestARevokedCopyIsFlaggedAndKeepsRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	running := installed(t, m)
	if running.Revoked != nil {
		t.Fatalf("%+v", running.Revoked)
	}
	go m.watchMarket(ctx, 5*time.Millisecond)
	f.mu.Lock()
	f.at = f.at.Add(time.Second)
	f.entries = nil
	f.revoked = []MarketRevocation{{ID: "weather", Version: "1.2.0", State: "withdrawn", Reason: "It leaks"}}
	f.mu.Unlock()
	eventually(t, "the poll to bring the news", func() bool { return m.Statuses()[0].Revoked != nil })
	got := m.Statuses()[0]
	if got.Revoked.State != "withdrawn" || got.Revoked.Reason != "It leaks" {
		t.Errorf("%+v", got.Revoked)
	}
	if !got.Running {
		t.Error("it was stopped without the owner choosing to")
	}

	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	again := newManager(t.TempDir(), t.TempDir(), m.store, hub(t))
	again.ctx = m.ctx
	t.Cleanup(func() { stopAll(again) })
	if err := again.SetMarketplace(f.url, f.pub, testApprovalPub); err != nil {
		t.Fatal(err)
	}
	again.loadAdded(m.ctx)
	eventually(t, "the news to be there after a restart", func() bool { s := again.Statuses(); return len(s) == 1 && s[0].Revoked != nil })
}

func TestTheOwnerCanStopIt(t *testing.T) {
	needApproval(t)
	f := newFakeMarket(t)
	m := marketManager(t, f)
	running := installed(t, m)
	if err := m.StopExtension("nope"); err == nil {
		t.Error("stopped nothing")
	}
	if err := m.StopExtension(running.Source); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to stop", func() bool { s := m.Statuses()[0]; return !s.Running && s.NeedsApproval })
	if grants, _ := m.store.Grants(context.Background(), "weather"); len(grants) != 0 {
		t.Errorf("a restart would start it: %v", grants)
	}
	if _, err := m.Approve(running.Source, m.Statuses()[0].Access); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run again", func() bool { return m.Statuses()[0].Running })
}

func TestAnUploadIsNotTheMarketplaceInstall(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	up, err := m.Upload(ctx, "x.zip", zipOf(t, map[string]string{entryFile: marketCode}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addSeen(m, ctx, up.Source, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to load", func() bool { return m.Statuses()[0].ID != "" })
	l := m.Marketplace(ctx).Entries[0]
	if l.Installed || !l.DifferentCopy || l.UpdateAvailable || l.InstalledSource != "" {
		t.Errorf("%+v", l)
	}
}

func TestAStoredMarketPackageIsCheckedAgain(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	found, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := packageKey(found.Source)
	// Someone with the database swaps the stored bytes, and the unpacked copy is gone.
	evil := archive(t, map[string]string{"package/" + entryFile: strings.Replace(marketCode, "api.example.com", "evil.example.com", 1)})
	if err := m.store.SavePackage(ctx, key, evil); err != nil {
		t.Fatal(err)
	}
	m.cacheDir = t.TempDir()
	if _, err := m.Inspect(ctx, found.Source); err == nil || !strings.Contains(err.Error(), "isn't the one the marketplace verified") {
		t.Errorf("got %v", err)
	}
}

func tarball(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: top + "/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return out.Bytes()
}

// Losing the stored copy can't quietly pick up a tag that moved.
func TestARefetchFromGitHubMustMatchTheFirstDownload(t *testing.T) {
	ctx := context.Background()
	code := `defineExtension({id:"g",name:"G",version:"1.0.0",data:{fetch(){return 1}}});`
	answer := tarball(t, "repo-v1", map[string]string{entryFile: code})
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(answer) }))
	defer github.Close()
	m := pageManager(t)
	m.archives = github.URL
	source := "github:acme/repo@v1"
	if _, err := m.Inspect(ctx, source); err != nil {
		t.Fatal(err)
	}
	lose := func() {
		sum := sha256.Sum256([]byte("acme/repo@v1"))
		if err := m.store.DeletePackage(ctx, hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
		m.cacheDir = t.TempDir()
	}
	// The same code compressed differently, under another top folder, is the same code.
	lose()
	answer = tarball(t, "repo-other-name", map[string]string{entryFile: code})
	if _, err := m.Inspect(ctx, source); err != nil {
		t.Errorf("the same code: %v", err)
	}
	lose()
	answer = tarball(t, "repo-v1", map[string]string{entryFile: strings.Replace(code, "return 1", "return 2", 1)})
	if _, err := m.Inspect(ctx, source); err == nil || !strings.Contains(err.Error(), "has changed since it was first downloaded") {
		t.Errorf("a moved tag: %v", err)
	}
}
