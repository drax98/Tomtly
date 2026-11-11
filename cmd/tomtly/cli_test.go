package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"tomtly/internal/auth"
	"tomtly/internal/store"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnvFallsBackOnlyWhenUnsetOrEmpty(t *testing.T) {
	t.Setenv("TOMTLY_TEST_VAR", "")
	if got := env("TOMTLY_TEST_VAR", "fallback"); got != "fallback" {
		t.Errorf("empty: %q", got)
	}
	t.Setenv("TOMTLY_TEST_VAR", "set")
	if got := env("TOMTLY_TEST_VAR", "fallback"); got != "set" {
		t.Errorf("set: %q", got)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("TOMTLY_TEST_SECRET", "s3cret")
	dir := writeConfig(t, `{"rooms":{"B":["x"],"A":[]},"allowedHosts":["${TOMTLY_TEST_SECRET}.example"]}`)
	cfg, err := loadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("timezone defaults to UTC, got %q", cfg.Timezone)
	}
	if strings.Join(cfg.RoomOrder, ",") != "B,A" || cfg.AllowedHosts[0] != "s3cret.example" {
		t.Errorf("%+v", cfg)
	}
}

func TestLoadConfigRefusals(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"unset variable": {`{"timezone":"${TOMTLY_TEST_UNSET_VAR}"}`, "TOMTLY_TEST_UNSET_VAR"},
		"bad json":       {`{`, "config.json"},
		"old adapters":   {`{"adapters":[]}`, "extensions now"},
		"old plugins":    {`{"plugins":{}}`, "extensions now"},
	} {
		dir := writeConfig(t, c.body)
		if _, err := loadConfig(filepath.Join(dir, "config.json")); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := loadConfig(filepath.Join(t.TempDir(), "config.json")); !os.IsNotExist(err) {
		t.Errorf("a missing file is reported as such: %v", err)
	}
}

func TestOpenStoreRefusesBadURLs(t *testing.T) {
	ctx := context.Background()
	if _, err := openStore(ctx, ""); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is not set") {
		t.Errorf("empty: %v", err)
	}
	// A cancelled context gives up at once instead of waiting out the two minutes, and never echoes the password.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := openStore(cancelled, "postgres://user:hunter2@127.0.0.1:1/db?sslmode=disable")
	if err == nil || !strings.Contains(err.Error(), "not reachable") || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("unreachable: %v", err)
	}
}

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	t.Setenv("LISTEN", "0.0.0.0:"+port)
	if err := healthcheck(); err != nil {
		t.Fatal(err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("503: %v", err)
	}
	t.Setenv("LISTEN", "no-port")
	if err := healthcheck(); err == nil {
		t.Error("a LISTEN without a port is refused")
	}
	srv.Close()
	t.Setenv("LISTEN", ":"+port)
	if err := healthcheck(); err == nil {
		t.Error("nothing is listening")
	}
}

func TestCLIDispatch(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LISTEN", "no-port")
	for _, c := range []struct {
		args       []string
		code       int
		serve      bool
		errContain string
	}{
		{nil, 0, true, ""},
		{[]string{"nope"}, 2, false, "usage: tomtly [reset-admin <username> | healthcheck]"},
		{[]string{"reset-admin"}, 2, false, "usage:"},
		{[]string{"reset-admin", "a", "b"}, 2, false, "usage:"},
		{[]string{"healthcheck", "x"}, 2, false, "usage:"},
		{[]string{"healthcheck"}, 1, false, "healthcheck:"},
		{[]string{"reset-admin", "ana"}, 1, false, "reset-admin: DATABASE_URL is not set"},
	} {
		var out, errw bytes.Buffer
		code, serve := cli(c.args, &out, &errw)
		if code != c.code || serve != c.serve || !strings.Contains(errw.String(), c.errContain) || out.Len() != 0 {
			t.Errorf("%v: code %d serve %v stderr %q stdout %q", c.args, code, serve, errw.String(), out.String())
		}
	}
}

func TestRunStartupFailures(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CONFIG_DIR", filepath.Join(t.TempDir(), "missing"))
	if err := run(); !os.IsNotExist(err) {
		t.Errorf("missing config folder: %v", err)
	}
	t.Setenv("CONFIG_DIR", writeConfig(t, `{"timezone":"Mars/Olympus"}`))
	if err := run(); err == nil || !strings.Contains(err.Error(), "timezone") {
		t.Errorf("bad timezone: %v", err)
	}
	t.Setenv("CONFIG_DIR", writeConfig(t, `{}`))
	if err := run(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is not set") {
		t.Errorf("no database url: %v", err)
	}
}

func testStore(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return url
}

func TestResetAdminAgainstPostgres(t *testing.T) {
	url := testStore(t)
	t.Setenv("DATABASE_URL", url)
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	name := "cli-reset-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	old, _ := auth.Hash("old password 12345")
	if err := st.CreateUser(ctx, store.User{ID: name, Username: name, Hash: old, Role: "member", Disabled: true, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := resetAdmin("nobody-"+name, &out, &errw); err == nil || !strings.Contains(err.Error(), "no user") {
		t.Errorf("unknown user: %v", err)
	}
	if err := resetAdmin(name, &out, &errw); err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(out.String())
	if password == "" || !strings.Contains(errw.String(), "New password for "+name) || strings.Contains(errw.String(), password) {
		t.Errorf("the password goes to stdout only: out %q err %q", out.String(), errw.String())
	}
	u, err := st.UserByName(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.Verify(u.Hash, password); !ok || u.Role != "admin" || u.Disabled {
		t.Errorf("%+v", u)
	}
}

// The whole service: start it against a real database, see it answer its health check, stop it with SIGTERM.
func TestRunServesAndStopsOnSIGTERM(t *testing.T) {
	url := testStore(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("DATABASE_URL", url)
	t.Setenv("CONFIG_DIR", writeConfig(t, `{"timezone":"Europe/Warsaw"}`))
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("WEB_DIR", t.TempDir())
	t.Setenv("SDK_DIR", t.TempDir())
	t.Setenv("LISTEN", addr)
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("MARKETPLACE_URL", "")
	oldLocal := time.Local
	defer func() { time.Local = oldLocal }()
	done := make(chan error, 1)
	go func() { done <- run() }()
	deadline := time.Now().Add(20 * time.Second)
	for healthcheck() != nil {
		select {
		case err := <-done:
			t.Fatalf("stopped before it served: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("never became healthy")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if time.Local.String() != "Europe/Warsaw" {
		t.Errorf("scripts' clock is %v", time.Local)
	}
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a clean stop is not an error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("did not stop")
	}
}

func TestRunRefusesBadTrustedProxies(t *testing.T) {
	url := testStore(t)
	t.Setenv("DATABASE_URL", url)
	t.Setenv("CONFIG_DIR", writeConfig(t, `{}`))
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("TRUSTED_PROXIES", "not-a-cidr")
	if err := run(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("%v", err)
	}
}
