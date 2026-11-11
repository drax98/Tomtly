package extension

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

const uploaded = `defineExtension({id:"up",name:"Up",version:"1",data:{fetch(c){return {ok:true}}}});`

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, text := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(text))
	}
	w.Close()
	return out.Bytes()
}

func uploadManager(t *testing.T) *Manager { return pageManager(t) }

func TestUploadFormats(t *testing.T) {
	for name, file := range map[string]struct {
		name string
		data []byte
	}{
		"ts":     {"my.ts", []byte(uploaded)},
		"zip":    {"x.ZIP", zipOf(t, map[string]string{"extension.ts": uploaded})},
		"tar.gz": {"x.tar.gz", archive(t, map[string]string{"pkg/extension.ts": uploaded})},
		"tgz":    {"x.tgz", archive(t, map[string]string{"extension.ts": uploaded})},
		"nested": {"x.zip", zipOf(t, map[string]string{"a/b/extension.ts": uploaded, "a/b/lib.ts": "", "a/other.txt": "x", "a/b/c/extension.ts": uploaded})},
	} {
		t.Run(name, func(t *testing.T) {
			m := uploadManager(t)
			got, err := m.Upload(context.Background(), file.name, file.data)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != "up" || !strings.HasPrefix(got.Source, "upload:") || len(got.Source) != len("upload:")+64 {
				t.Fatalf("got %+v", got)
			}
			if len(m.Statuses()) != 0 {
				t.Fatal("uploading must not start anything")
			}
		})
	}
}

func TestUploadRefusals(t *testing.T) {
	var big bytes.Buffer
	gz := gzip.NewWriter(&big)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "p/extension.ts", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(uploaded))})
	tw.Write([]byte(uploaded))
	tw.WriteHeader(&tar.Header{Name: "p/big", Typeflag: tar.TypeReg, Mode: 0o644, Size: maxDownload + 1})
	tw.Write(make([]byte, maxDownload+1)) // compresses to almost nothing: a bomb
	tw.Close()
	gz.Close()

	for want, file := range map[string]struct {
		name string
		data []byte
	}{
		"an unsafe path":       {"x.zip", zipOf(t, map[string]string{"../evil": "x", "extension.ts": uploaded})},
		"has an unsafe path":   {"x.tar.gz", archive(t, map[string]string{"extension.ts": uploaded}, tar.Header{Name: "../../evil", Typeflag: tar.TypeReg, Mode: 0o644})},
		"no extension.ts":      {"x.zip", zipOf(t, map[string]string{"a.ts": "x"})},
		"more than one":        {"x.zip", zipOf(t, map[string]string{"a/extension.ts": uploaded, "b/extension.ts": uploaded})},
		"over 64 MiB":          {"x.zip", zipOf(t, map[string]string{"extension.ts": uploaded, "big": strings.Repeat("0", maxDownload+1)})},
		"unpacks to over":      {"x.tgz", big.Bytes()},
		"upload a .zip":        {"x.exe", []byte("x")},
		"not a zip":            {"x.zip", []byte("nope")},
		"the file is over 16 ": {"x.ts", make([]byte, MaxUpload+1)},
	} {
		m := uploadManager(t)
		_, err := m.Upload(context.Background(), file.name, file.data)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestUploadDropsLinks(t *testing.T) {
	data := archive(t, map[string]string{"extension.ts": uploaded}, tar.Header{Name: "link.ts", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	m := uploadManager(t)
	got, err := m.Upload(context.Background(), "x.tgz", data)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := m.resolve(context.Background(), got.Source)
	if err != nil {
		t.Fatal(err)
	}
	if entries := folderNames(t, dir); len(entries) != 1 || entries[0] != entryFile {
		t.Fatalf("got %v", entries)
	}
}

func TestBrokenUploadIsNotKept(t *testing.T) {
	m := uploadManager(t)
	data := []byte(`defineExtension({id:"Bad Id"});`)
	if _, err := m.Upload(context.Background(), "x.ts", data); err == nil {
		t.Fatal("a broken extension must be refused")
	}
	sum := sha256.Sum256(data)
	if stored(t, m, hex.EncodeToString(sum[:])) {
		t.Fatal("the refused package was left in the store")
	}
}

func TestUploadSurvivesARestartAndRemoveDeletesIt(t *testing.T) {
	m := uploadManager(t)
	ctx := context.Background()
	got, err := m.Upload(ctx, "x.ts", []byte(uploaded))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addSeen(m, ctx, got.Source, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "data", func() bool { d, _, _ := m.plugins.Get("up"); return string(d) == `{"ok":true}` })

	again := newManager(m.configDir, t.TempDir(), m.store, hub(t))

	t.Cleanup(func() { stopAll(again) })
	again.ctx = m.ctx
	again.loadAdded(m.ctx)
	eventually(t, "it to come back from the store", func() bool { d, _, _ := again.plugins.Get("up"); return d != nil })
	if err := again.Remove(got.Source); err != nil {
		t.Fatal(err)
	}
	if stored(t, m, got.Source) {
		t.Fatal("removing it must delete the package")
	}
	if err := m.Remove(got.Source); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveKeepsAPackageAnotherInstanceUses(t *testing.T) {
	m := uploadManager(t)
	got, err := m.Upload(context.Background(), "x.ts", []byte(uploaded))
	if err != nil {
		t.Fatal(err)
	}
	m.add(m.ctx, entry{Source: got.Source})
	m.add(m.ctx, entry{Source: got.Source})
	if err := m.Remove(got.Source); err != nil {
		t.Fatal(err)
	}
	if !stored(t, m, got.Source) {
		t.Fatal("the package is still in use")
	}
}

func stored(t *testing.T, m *Manager, source string) bool {
	t.Helper()
	_, err := m.store.Package(context.Background(), strings.TrimPrefix(source, "upload:"))
	return err == nil
}

func folderNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
