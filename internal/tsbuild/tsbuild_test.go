package tsbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBundleScriptBundlesItsOwnCode(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "lib/a.ts", `import { b } from "./b"; export const a = b + 1;`)
	write(t, dir, "lib/b.js", `export const b = 41;`)
	code, err := BundleScript(dir, "main", `import { a } from "./lib/a"; export default a;`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "41") {
		t.Errorf("got %s", code)
	}
}

// Extensions share this code and keep what they could do, including importing their own data files.
func TestBundleKeepsExtensionsWorkingAndInTheirFolder(t *testing.T) {
	root := t.TempDir()
	write(t, root, "secret.json", `{"key":"s3cret"}`)
	dir := filepath.Join(root, "ext")
	write(t, dir, "data.json", `{"name":"kept"}`)
	code, err := Bundle(dir, "extension", `import d from "./data.json"; export default d;`)
	if err != nil || !strings.Contains(code, "kept") {
		t.Fatalf("%s %v", code, err)
	}
	if code, err := Bundle(dir, "extension", `import d from "../secret.json"; export default d;`); err == nil || strings.Contains(code, "s3cret") {
		t.Fatalf("an extension read outside its folder: %s", code)
	}
}

func TestBundleScriptIgnoresTsconfig(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tsconfig.json", `{"compilerOptions":{"verbatimModuleSyntax":true,"alwaysStrict":true}}`)
	dir := filepath.Join(root, "scripts")
	write(t, dir, "x.ts", ``)
	code, err := BundleScript(dir, "main", `const a = 1; export default a;`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "use strict") {
		t.Errorf("a tsconfig above the folder changed the output: %s", code)
	}
}

func TestBundleErrorsNameTheLine(t *testing.T) {
	_, err := BundleScript(t.TempDir(), "main", "const ok = 1;\nconst = ;")
	if err == nil || !strings.HasPrefix(err.Error(), "main.ts:2: ") {
		t.Errorf("a syntax error should say which script and line: %v", err)
	}
}

// A script folder reached through a link still bundles its own nested imports.
func TestBundleScriptThroughALinkedFolder(t *testing.T) {
	real := t.TempDir()
	write(t, real, "lib/a.ts", `import { b } from "./b"; export const a = b + 1;`)
	write(t, real, "lib/b.ts", `export const b = 41;`)
	link := filepath.Join(t.TempDir(), "scripts")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	code, err := BundleScript(link, "main", `import { a } from "./lib/a"; export default a;`)
	if err != nil || !strings.Contains(code, "41") {
		t.Fatalf("%v %s", err, code)
	}
}
