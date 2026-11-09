package extension

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"home-control/internal/store"
)

const marketCode = `defineExtension({id:"weather",name:"Weather",version:"1.2.0",access:{network:["api.example.com"]},data:{fetch(c){return {ok:true}}}});`

// The approval key and its public half, shared by every fake here, as one marketplace's would be.
var (
	testApprovalPubKey, testApprovalPriv, _ = ed25519.GenerateKey(nil)
	testApprovalPub                         = base64.StdEncoding.EncodeToString(testApprovalPubKey)
)

// signedEntries is the entries as they appear in a catalog, each with the approval key's signature over its exact bytes.
func signedEntries(entries []MarketEntry) []map[string]string {
	out := []map[string]string{}
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		out = append(out, map[string]string{"entry": base64.StdEncoding.EncodeToString(raw), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(testApprovalPriv, raw))})
	}
	return out
}

func catalogPayload(at, expires time.Time, entries []MarketEntry, revoked []MarketRevocation) []byte {
	payload, _ := json.Marshal(map[string]any{"generatedAt": at.Format(time.RFC3339Nano), "signedAt": at.Format(time.RFC3339Nano), "expiresAt": expires.Format(time.RFC3339Nano),
		"entries": signedEntries(entries), "revoked": revoked})
	return payload
}

type fakeMarket struct {
	t        *testing.T
	priv     ed25519.PrivateKey
	pub      string
	mu       sync.Mutex
	at       time.Time
	expires  time.Time // zero means a week after at
	entries  []MarketEntry
	revoked  []MarketRevocation
	unsigned bool // the entries come with signatures by a key that isn't the approval key
	packages map[string][]byte
	down     bool
	tamper   bool
	url      string
	extra    map[string]string // more files for the next package, such as migrations
}

func newFakeMarket(t *testing.T) *fakeMarket {
	pub, priv, _ := ed25519.GenerateKey(nil)
	f := &fakeMarket{t: t, priv: priv, pub: base64.StdEncoding.EncodeToString(pub), at: time.Now().UTC(), packages: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.add(marketCode, "Internet: api.example.com")
	f.url = srv.URL
	return f
}

// add publishes code the way the marketplace would, with the access labels given.
func (f *fakeMarket) add(code string, access ...string) MarketEntry {
	return f.addAs("weather", "1.2.0", code, access...)
}

func (f *fakeMarket) addAs(id, version, code string, access ...string) MarketEntry {
	files := map[string]string{"package/" + entryFile: code}
	for name, text := range f.extra {
		files["package/"+name] = text
	}
	pkg := archive(f.t, files)
	sum := sha256.Sum256(pkg)
	e := MarketEntry{ID: id, Name: id, Description: "d", Kind: "plugin", Version: version, Author: "a", Provides: []string{"data"}, Access: access,
		TestedOn: "Simulator only", ApprovedOn: "2026-01-02", SHA256: hex.EncodeToString(sum[:]), Size: len(pkg), Package: "/v1/packages/" + id + "-" + version + ".tar.gz", Repository: "https://example.com/a/" + id}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries[:0:0], f.entries...)
	for i, old := range f.entries {
		if old.ID == id {
			f.entries = append(f.entries[:i], f.entries[i+1:]...)
			break
		}
	}
	f.entries = append(f.entries, e)
	f.packages[e.Package] = pkg
	return e
}

func (f *fakeMarket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/v1/catalog" {
		expires := f.expires
		if expires.IsZero() {
			expires = f.at.Add(7 * 24 * time.Hour)
		}
		payload := catalogPayload(f.at, expires, f.entries, f.revoked)
		if f.unsigned {
			_, forger, _ := ed25519.GenerateKey(nil)
			raw, _ := json.Marshal(f.entries[0])
			payload, _ = json.Marshal(map[string]any{"generatedAt": f.at.Format(time.RFC3339Nano), "signedAt": f.at.Format(time.RFC3339Nano), "expiresAt": expires.Format(time.RFC3339Nano),
				"entries": []map[string]string{{"entry": base64.StdEncoding.EncodeToString(raw), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(forger, raw))}}})
		}
		sig := ed25519.Sign(f.priv, payload)
		if f.tamper {
			payload = []byte(strings.Replace(string(payload), "generatedAt", "generatedaT", 1))
		}
		json.NewEncoder(w).Encode(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(sig)})
		return
	}
	if pkg, ok := f.packages[r.URL.Path]; ok {
		w.Write(pkg)
		return
	}
	http.NotFound(w, r)
}

func marketManager(t *testing.T, f *fakeMarket) *Manager {
	m := pageManager(t)
	if err := m.SetMarketplace(f.url, f.pub, testApprovalPub); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMarketCatalog(t *testing.T) {
	f := newFakeMarket(t)
	m := marketManager(t, f)
	view := m.Marketplace(context.Background())
	if !view.Configured || !view.Reachable || !view.Verified || len(view.Entries) != 1 || view.Entries[0].TestedOn != "Simulator only" || view.Entries[0].Installed {
		t.Fatalf("got %+v", view)
	}
}

func TestMarketCatalogRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("not set up", func(t *testing.T) {
		if v := pageManager(t).Marketplace(ctx); v.Configured || v.Reachable || len(v.Entries) != 0 {
			t.Fatalf("got %+v", v)
		}
		if err := pageManager(t).SetMarketplace("http://x", "not a key", testApprovalPub); err == nil {
			t.Fatal("a bad key was accepted")
		}
	})
	t.Run("tampered", func(t *testing.T) {
		f := newFakeMarket(t)
		f.tamper = true
		v := marketManager(t, f).Marketplace(ctx)
		if !v.Reachable || v.Verified || len(v.Entries) != 0 || v.Error == "" {
			t.Fatalf("got %+v", v)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		f := newFakeMarket(t)
		other, _, _ := ed25519.GenerateKey(nil)
		m := pageManager(t)
		m.SetMarketplace(f.url, base64.StdEncoding.EncodeToString(other), testApprovalPub)
		if v := m.Marketplace(ctx); v.Verified || len(v.Entries) != 0 {
			t.Fatalf("got %+v", v)
		}
	})
	t.Run("replayed", func(t *testing.T) {
		f := newFakeMarket(t)
		m := marketManager(t, f)
		if !m.Marketplace(ctx).Verified {
			t.Fatal("first catalog refused")
		}
		f.at = f.at.Add(-time.Hour)
		v := m.Marketplace(ctx)
		if v.Verified || len(v.Entries) != 0 || !strings.Contains(v.Error, "older") {
			t.Fatalf("got %+v", v)
		}
		if _, err := m.MarketInspect(ctx, "weather"); err == nil {
			t.Fatal("installed from a replayed catalog")
		}
	})
	t.Run("down", func(t *testing.T) {
		f := newFakeMarket(t)
		f.down = true
		v := marketManager(t, f).Marketplace(ctx)
		if !v.Configured || v.Reachable || v.Error == "" || len(v.Entries) != 0 {
			t.Fatalf("got %+v", v)
		}
	})
	t.Run("entry pointing elsewhere is left out", func(t *testing.T) {
		f := newFakeMarket(t)
		f.entries[0].Package = "/v1/packages/../../evil.tar.gz"
		if v := marketManager(t, f).Marketplace(ctx); len(v.Entries) != 0 || !v.Verified {
			t.Fatalf("got %+v", v)
		}
	})
}

func TestMarketInstallRefusals(t *testing.T) {
	ctx := context.Background()
	for want, setup := range map[string]func(f *fakeMarket){
		"checksum": func(f *fakeMarket) { f.packages[f.entries[0].Package] = append(f.packages[f.entries[0].Package], 0) },
		"not \"weather\"": func(f *fakeMarket) {
			f.add(strings.Replace(marketCode, `id:"weather"`, `id:"other"`, 1), "Internet: api.example.com")
		},
		"1.2.0": func(f *fakeMarket) {
			f.add(strings.Replace(marketCode, `"1.2.0"`, `"9.9.9"`, 1), "Internet: api.example.com")
		},
		"really asks for": func(f *fakeMarket) {
			f.add(strings.Replace(marketCode, `api.example.com`, `evil.example.com`, 1), "Internet: api.example.com")
		},
		"access the marketplace listed": func(f *fakeMarket) { f.add(marketCode) },
	} {
		t.Run(want, func(t *testing.T) {
			f := newFakeMarket(t)
			setup(f)
			m := marketManager(t, f)
			_, err := m.MarketInspect(ctx, "weather")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("got %v", err)
			}
			if _, err := m.store.Package(ctx, "market-"+f.entries[0].SHA256); err == nil {
				t.Error("a refused package stayed stored")
			}
		})
	}
	t.Run("unknown id", func(t *testing.T) {
		if _, err := marketManager(t, newFakeMarket(t)).MarketInspect(ctx, "nope"); err == nil {
			t.Fatal("inspected an id that isn't listed")
		}
	})
}

func TestMarketInstallGoesThroughApprovalAndSurvivesWithoutIt(t *testing.T) {
	approveAll.Store(false)
	t.Cleanup(func() { approveAll.Store(true) })
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	found, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(found.Source, "market:weather@1.2.0#") || len(m.Statuses()) != 0 {
		t.Fatalf("got %+v", found)
	}
	if _, err := addSeen(m, ctx, "market:weather@1.2.0#"+strings.Repeat("0", 64), nil); err == nil {
		t.Fatal("added a package that was never downloaded")
	}
	if _, err := addSeen(m, ctx, found.Source, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to load", func() bool { return m.Statuses()[0].ID != "" })
	if v := m.Marketplace(ctx); !v.Entries[0].Installed || v.Entries[0].InstalledVersion != "1.2.0" || v.Entries[0].UpdateAvailable {
		t.Fatalf("got %+v", v.Entries)
	}
	grants, _ := m.store.Grants(ctx, "weather")
	if len(grants) != 1 || grants[0] != "network:api.example.com" {
		t.Fatalf("grants %v", grants) // what the code asks for, given because the owner pressed Add
	}

	// The marketplace is gone and the cache is lost; the same store must be enough.
	f.down = true
	restart := func() *Manager {
		r := newManager(t.TempDir(), t.TempDir(), m.store, hub(t))
		t.Cleanup(func() { stopAll(r) })
		r.ctx = m.ctx
		r.loadAdded(m.ctx)
		return r
	}
	r := restart()
	eventually(t, "it to run without the marketplace", func() bool { s := r.Statuses(); return len(s) == 1 && s[0].Running })

	// Without a grant it waits for the owner, like any other extension.
	m.store.SetGrants(ctx, "weather", nil)
	r = restart()
	eventually(t, "it to wait for approval", func() bool { s := r.Statuses(); return len(s) == 1 && s[0].NeedsApproval && !s[0].Running })
	if _, err := approveSeen(r, found.Source); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to run once approved", func() bool { return r.Statuses()[0].Running })
}

func TestMarketUpdateKeepsSettingsAndDropsTheOldPackage(t *testing.T) {
	ctx := context.Background()
	f := newFakeMarket(t)
	m := marketManager(t, f)
	first, _ := m.MarketInspect(ctx, "weather")
	if _, err := addSeen(m, ctx, first.Source, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "it to load", func() bool { return m.Statuses()[0].ID != "" })
	f.addAs("weather", "1.10.0", strings.Replace(marketCode, `"1.2.0"`, `"1.10.0"`, 1), "Internet: api.example.com")
	view := m.Marketplace(ctx)
	if !view.Entries[0].UpdateAvailable || view.Entries[0].InstalledSource != first.Source {
		t.Fatalf("1.10.0 is newer than 1.2.0: %+v", view.Entries[0])
	}
	second, err := m.MarketInspect(ctx, "weather")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replaceSeen(m, ctx, first.Source, second.Source, nil); err != nil {
		t.Fatal(err)
	}
	oldKey, _ := packageKey(first.Source)
	if _, err := m.store.Package(ctx, oldKey); err != store.ErrNotFound {
		t.Errorf("the old package stayed: %v", err)
	}
	if m.Marketplace(ctx).Entries[0].UpdateAvailable {
		t.Error("still offers the update")
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.10.0", "1.2.0", true}, {"1.2.0", "1.10.0", false}, {"1.2.0", "1.2.0", false}, {"v2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.0-rc.1", true}, {"1.0.0-rc.2", "1.0.0-rc.10", false}, {"1.0.0-rc.1", "1.0.0", false}, {"1.0.0+b", "1.0.0", false},
		{"2", "1.0.0", false}, {"1.0.0", "x", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("%s > %s: got %v", c.a, c.b, got)
		}
	}
}

func TestMarketSourceFormat(t *testing.T) {
	sha := strings.Repeat("a", 64)
	for source, ok := range map[string]bool{
		"market:weather@1.2.0#" + sha: true, "market:weather@1.2.0": false, "market:Weather@1.2.0#" + sha: false,
		"market:weather@1.2.0#" + sha[1:]: false, "market:weather@../x#" + sha: false, "market:a/b@1#" + sha: false,
	} {
		if got := checkPageSource(source) == nil; got != ok {
			t.Errorf("%s: got %v", source, got)
		}
	}
}

func TestMarketAddress(t *testing.T) {
	for address, ok := range map[string]bool{
		"https://market.example.com":       true,
		"http://market.example.com":        false,
		"http://8.8.8.8:8100":              false,
		"http://127.0.0.1:8100":            true,
		"http://192.168.1.5:8100":          true,
		"http://marketplace:8100":          true,
		"http://host.docker.internal:8100": true,
		"ftp://market.example.com":         false,
		"market.example.com":               false,
	} {
		if err := checkMarketAddress(address); (err == nil) != ok {
			t.Errorf("%s: %v", address, err)
		}
	}
}
