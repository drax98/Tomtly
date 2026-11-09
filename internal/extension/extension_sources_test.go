package extension

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"home-control/internal/store"
	"home-control/plugin"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallsFromGitHub(t *testing.T) {
	const extension = `defineExtension({id:"remote",name:"Remote",version:"1",data:{fetch(){return "hello"}}});`
	var requests []string
	data := archive(t, map[string]string{"repo-abc/" + entryFile: `throw new Error("wrong folder")`, "repo-abc/sub/dir/" + entryFile: extension, "repo-abc/sub/dir/types.d.ts": "interface R {}"})
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		w.Write(data)
	}))
	defer github.Close()
	useFake := func(m *Manager) { m.archives = github.URL }

	m := run(t, "github:acme/repo/sub/dir@v1.2.0", nil, useFake)
	eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("remote"); return string(d) == `"hello"` })
	if len(requests) != 1 || requests[0] != "/acme/repo/tar.gz/v1.2.0" {
		t.Fatalf("got %v", requests)
	}
	if m.Types()["extensions/remote/types.d.ts"] == "" {
		t.Error("types should come along")
	}

	// A second start uses what it has, so the code can't change until you change the ref, and works with no internet.
	github.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again := newManager(t.TempDir(), t.TempDir(), m.store, hub(t))
	t.Cleanup(func() { stopAll(again) })
	again.archives = github.URL
	again.add(ctx, entry{Source: "github:acme/repo/sub/dir@v1.2.0"}, false)
	eventually(t, "data from the copy", func() bool { d, _, _ := again.plugins.Get("remote"); return string(d) == `"hello"` })
}

func TestBadSourcesAreRefused(t *testing.T) {
	for want, source := range map[string]string{
		"must look like":    "github:acme/repo", // no ref: it could change by itself
		"bad path":          "github:acme/repo/../secrets@v1",
		"inside the config": "elsewhere/folder",
		"no " + entryFile:   "./missing",
	} {
		t.Run(want, func(t *testing.T) { failsWith(t, run(t, source, nil), want) })
	}
}

func TestDownloadFailureShowsAndChangesNothing(t *testing.T) {
	github := httptest.NewServer(http.NotFoundHandler())
	defer github.Close()
	m := run(t, "github:acme/repo@v1", nil, func(m *Manager) { m.archives = github.URL })
	failsWith(t, m, "404")
	if entries, _ := os.ReadDir(filepath.Join(m.cacheDir, "extensions")); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestExtractIsSafe(t *testing.T) {
	into := t.TempDir()
	outside := filepath.Join(filepath.Dir(into), "pwned")
	defer os.Remove(outside)
	err := extract(bytes.NewReader(archive(t, map[string]string{"top/../../pwned": "x"})), into)
	if err == nil {
		t.Error("a path leaving the folder must fail")
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Error("wrote outside the folder")
	}

	into = t.TempDir()
	data := archive(t, map[string]string{"top/ok.txt": "x"}, tar.Header{Name: "top/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	if err := extract(bytes.NewReader(data), into); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(into, "link")); err == nil {
		t.Error("a link must not be unpacked")
	}
	if _, err := os.Stat(filepath.Join(into, "ok.txt")); err != nil {
		t.Error(err)
	}
}

func TestAddFromThePageSurvivesARestart(t *testing.T) {
	m := pageManager(t)
	ctx := context.Background()
	got, err := m.Inspect(ctx, "./extensions/mine")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "mine" || got.Network[0] != "a.example.com" || len(got.Settings) != 3 || got.Settings[0].Key != "key" || got.Settings[0].Set {
		t.Fatalf("got %+v", got)
	}
	if d, _, _ := m.plugins.Get("mine"); d != nil {
		t.Fatal("inspecting must not start it")
	}
	if len(m.Statuses()) != 0 {
		t.Fatal("inspecting must not add it")
	}
	if _, err := addSeen(m, ctx, "./extensions/mine", map[string]any{}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("a missing required setting must be refused: %v", err)
	}
	if _, err := addSeen(m, ctx, "./extensions/mine", map[string]any{"key": "the-secret", "note": ""}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("mine"); return string(d) == `{"on":true}` })
	if !status(m).Removable {
		t.Error("it should be removable")
	}
	data, _ := json.Marshal(m.Statuses())
	if strings.Contains(string(data), "the-secret") {
		t.Errorf("the status shows a secret: %s", data)
	}
	if _, err := addSeen(m, ctx, "./extensions/mine", map[string]any{"key": "x"}); err == nil {
		t.Error("adding the same source twice must be refused")
	}
	again := newManager(m.configDir, t.TempDir(), m.store, hub(t))
	t.Cleanup(func() { stopAll(again) })
	again.ctx = m.ctx
	again.loadAdded(m.ctx)
	eventually(t, "it to come back", func() bool { d, _, _ := again.plugins.Get("mine"); return d != nil })
	again.Remove("./extensions/mine")

	if err := m.Remove("./extensions/mine"); err != nil {
		t.Fatal(err)
	}
	if len(m.Statuses()) != 0 {
		t.Error("it should be gone")
	}
	if d, _, ok := m.plugins.Get("mine"); ok || d != nil {
		t.Error("its data should be gone")
	}
	if saved, _ := m.store.AddedExtensions(context.Background()); len(saved) != 0 {
		t.Error("removing it must forget its settings")
	}
}

func TestPageCantAskForAnyFolder(t *testing.T) {
	m := pageManager(t)
	for _, source := range []string{"/config/secrets", "../secrets", "./../secrets", "./a/../../b", "http://example.com/x", "github:a/b"} {
		if _, err := m.Inspect(context.Background(), source); err == nil {
			t.Errorf("%s must be refused", source)
		}
	}
}

func TestRemovingOneFromConfigLastsUntilItIsAddedBack(t *testing.T) {
	needApproval(t)
	ctx := context.Background()
	m := pageManager(t)
	source := "./extensions/mine"
	write(t, m.configDir, "extensions/mine/"+entryFile, `defineExtension({id:"mine",name:"Mine",version:"1",access:{storage:true},devices:{poll(){return []}}});`)
	e := entry{Source: source}
	m.configured = []entry{e}
	inst := m.add(m.ctx, e, false)
	eventually(t, "it to load", func() bool { return inst.currentStatus().ID == "mine" })
	if err := m.store.SetGrants(ctx, "mine", []string{"storage"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(source); err != nil {
		t.Fatal(err)
	}
	if len(m.Statuses()) != 0 {
		t.Fatalf("still listed: %+v", m.Statuses())
	}
	if grants, _ := m.store.Grants(ctx, "mine"); len(grants) != 0 {
		t.Errorf("its approval outlived it: %v", grants)
	}
	removed, err := m.RemovedExtensions(ctx)
	if err != nil || len(removed) != 1 || removed[0].Kind != "integration" || removed[0].Name != "Mine" {
		t.Fatalf("offered back: %+v %v", removed, err)
	}

	// A restart reads the same file and must not bring it back.
	raw, _ := json.Marshal(e)
	again, err := Start(ctx, []json.RawMessage{raw}, m.configDir, t.TempDir(), m.store, plugin.NewHub())
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Statuses()) != 0 {
		t.Fatalf("back after a restart: %+v", again.Statuses())
	}
	if err := m.RestoreExtension(ctx, "./extensions/other"); err == nil {
		t.Error("restored something config.json doesn't list")
	}
	if err := m.RestoreExtension(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreExtension(ctx, source); err == nil {
		t.Error("restored it twice")
	}
	eventually(t, "it to wait for approval", func() bool {
		s := m.Statuses()
		return len(s) == 1 && s[0].ID == "mine" && s[0].NeedsApproval
	})
	if removed, _ := m.RemovedExtensions(ctx); len(removed) != 0 {
		t.Errorf("still offered: %+v", removed)
	}
}

func TestRemovingStopsARunawayExtension(t *testing.T) {
	m := pageManager(t)
	write(t, m.configDir, "extensions/loop/"+entryFile, `defineExtension({id:"loop",name:"L",version:"1",data:{fetch(){while(true){}}}});`)
	if _, err := addSeen(m, context.Background(), "./extensions/loop", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	if err := m.Remove("./extensions/loop"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > stopWait/2 {
		t.Errorf("took %v", time.Since(started))
	}
}

func TestCryptoMatchesKnownVectors(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",data:{fetch(c){
		const k = c.crypto, abc = k.hex("abc");
		const key = "000102030405060708090a0b0c0d0e0f", block = "00112233445566778899aabbccddeeff";
		const cbc = k.encrypt({mode:"cbc",key,iv:key,data:k.hex("hello"),padding:true});
		const gcm = k.encrypt({mode:"gcm",key,iv:"000000000000000000000000",data:abc,aad:abc});
		return {
			md5: k.hash("md5", abc),
			sha256: k.hash("sha256", abc),
			hmac: k.hmac("sha256", k.hex("key"), k.hex("The quick brown fox jumps over the lazy dog")),
			ecb: k.encrypt({mode:"ecb",key,data:block}),
			ecbBack: k.decrypt({mode:"ecb",key,data:k.encrypt({mode:"ecb",key,data:block})}),
			cbcBack: k.text(k.decrypt({mode:"cbc",key,iv:key,data:cbc,padding:true})),
			gcmBack: k.text(k.decrypt({mode:"gcm",key,iv:"000000000000000000000000",data:gcm,aad:abc})),
			b64: k.toBase64(abc),
			b64Back: k.text(k.fromBase64("YWJj")),
			random: k.random(8).length,
		};
	}}});`), nil)
	var got map[string]any
	eventually(t, "data", func() bool {
		d, _, _ := m.plugins.Get("a")
		return d != nil && json.Unmarshal(d, &got) == nil
	})
	for key, want := range map[string]any{
		"md5":     "900150983cd24fb0d6963f7d28e17f72",
		"sha256":  "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"hmac":    "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
		"ecb":     "69c4e0d86a7b0430d8cdb78070b4c55a", // FIPS-197
		"ecbBack": "00112233445566778899aabbccddeeff",
		"cbcBack": "hello",
		"gcmBack": "abc",
		"b64":     "YWJj",
		"b64Back": "abc",
		"random":  16.0,
	} {
		if got[key] != want {
			t.Errorf("%s: got %v, want %v", key, got[key], want)
		}
	}
}

func TestUDPAndTCPReachDeclaredHosts(t *testing.T) {
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buf := make([]byte, 64)
		n, from, err := udp.ReadFrom(buf)
		if err == nil {
			udp.WriteTo(append([]byte("pong:"), buf[:n]...), from)
		}
	}()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		conn.Write(append([]byte("echo:"), buf[:n]...))
	}()

	udpPort, tcpPort := udp.LocalAddr().(*net.UDPAddr).Port, listener.Addr().(*net.TCPAddr).Port
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{network:["{host}"],sockets:true},`+hostSettings+`,data:{everySeconds:3600,fetch(c){
		const k = c.crypto, s = c.settings;
		const u = k.text(c.udp.request(s.host, s.port, k.hex("ping")));
		const conn = c.tcp.connect(s.host, `+fmt.Sprint(tcpPort)+`);
		conn.write(k.hex("hi"));
		const t = k.text(conn.read());
		conn.close();
		return {u, t};
	}}});`), map[string]any{"host": "127.0.0.1", "port": float64(udpPort)}, allowLoopback)
	eventually(t, "replies", func() bool {
		d, _, _ := m.plugins.Get("a")
		return string(d) == `{"u":"pong:ping","t":"echo:hi"}`
	})
}

func TestSocketsObeyTheNetworkList(t *testing.T) {
	n := newNetwork(accessDecl{Network: []string{"public.example.com", "{host}"}}, map[string]any{"host": "10.0.0.2:80"}, false)
	if private, err := n.socketRule("10.0.0.2", 80); err != nil || !private {
		t.Errorf("a host from a setting may be private: %v %v", private, err)
	}
	if private, err := n.socketRule("public.example.com", 80); err != nil || private {
		t.Errorf("a named host stays public: %v %v", private, err)
	}
	for _, host := range []string{"10.0.0.3", "elsewhere.example.com", "127.0.0.1"} {
		if _, err := n.socketRule(host, 80); err == nil {
			t.Errorf("%s isn't on the list", host)
		}
	}

	lan := newNetwork(accessDecl{Lan: true}, nil, false)
	if private, err := lan.socketRule("192.168.1.9", 80); err != nil || !private {
		t.Errorf(`"lan" should reach the local network: %v %v`, private, err)
	}
	for _, host := range []string{"8.8.8.8", "127.0.0.1", "169.254.169.254", "example.com"} {
		if _, err := lan.socketRule(host, 80); err == nil {
			t.Errorf(`"lan" must not reach %s`, host)
		}
	}
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",access:{sockets:true},data:{fetch(c){return c.udp.discover(1234,"00")}}});`), nil)
	failsWith(t, m, `needs "lan"`)
}

func TestStorageSurvivesARestart(t *testing.T) {
	source := `defineExtension({id:"a",name:"a",version:"1",access:{storage:true},data:{everySeconds:3600,fetch(c){
		const seen = c.storage.get("runs") || 0;
		c.storage.set("runs", seen + 1);
		return {seen};
	}}});`
	dir, data := folder(t, source), store.NewMemory()
	for want, wantData := range []string{`{"seen":0}`, `{"seen":1}`} {
		ctx, cancel := context.WithCancel(context.Background())
		m := newManager(t.TempDir(), t.TempDir(), data, hub(t))
		t.Cleanup(func() { stopAll(m) })
		inst := m.add(ctx, entry{Source: dir}, false)
		eventually(t, "run "+fmt.Sprint(want), func() bool { d, _, _ := m.plugins.Get("a"); return string(d) == wantData })
		cancel()
		<-inst.done
	}
}

func TestActionsRunOnTheExtension(t *testing.T) {
	m := run(t, folder(t, `const sent = []; defineExtension({id:"a",name:"a",version:"1",
		data:{everySeconds:1,fetch(){return sent}},
		actions:{
			send(c, args) { sent.push(args.text); },
			fail() { throw new Error("no such chat"); },
		}});`), nil)
	eventually(t, "the plugin", func() bool { d, _, _ := m.plugins.Get("a"); return d != nil })
	if ps := m.plugins.Statuses(); len(ps[0].Actions) != 2 {
		t.Errorf("actions should be listed: %+v", ps[0])
	}
	if err := m.plugins.Act("a", "nope", []byte("null")); err == nil {
		t.Error("an unknown action must be refused")
	}
	if err := m.plugins.Act("a", "send", []byte(`{"text":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the action", func() bool { d, _, _ := m.plugins.Get("a"); return string(d) == `["hello"]` })
	if err := m.plugins.Act("a", "fail", []byte("null")); err != nil {
		t.Fatal(err)
	}
	failsWith(t, m, "no such chat")
}

func TestActionsAloneAreEnough(t *testing.T) {
	m := run(t, folder(t, `defineExtension({id:"a",name:"a",version:"1",actions:{ping(){}}});`), nil)
	eventually(t, "it to run", func() bool { return status(m).Running })
	if status(m).Provides[0] != "actions" {
		t.Errorf("got %+v", status(m).Provides)
	}
}

func TestExtractRefusesPilesOfEntriesAndDeepFolders(t *testing.T) {
	many := map[string]string{}
	for n := range maxArchiveEntries + 1 {
		many[fmt.Sprintf("top/f%d", n)] = ""
	}
	if err := extract(bytes.NewReader(archive(t, many)), t.TempDir()); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("too many entries: %v", err)
	}
	deep := "top/" + strings.Repeat("d/", maxArchiveDepth) + "f"
	if err := extract(bytes.NewReader(archive(t, map[string]string{deep: "x"})), t.TempDir()); err == nil || !strings.Contains(err.Error(), "deep") {
		t.Errorf("too deep: %v", err)
	}
	ok := "top/" + strings.Repeat("d/", maxArchiveDepth-1) + "f"
	if err := extract(bytes.NewReader(archive(t, map[string]string{ok: "x"})), t.TempDir()); err != nil {
		t.Errorf("just deep enough: %v", err)
	}
}
