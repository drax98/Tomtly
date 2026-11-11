// Command tomtly is one local service for every smart device at home: an API, a page, automations, and the extensions that bring in devices and data.
// Everything about a particular home lives in its config directory, so it runs the same on any machine, in Docker or not.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata" // the timezone database, so schedules work on minimal images

	"tomtly/device"
	"tomtly/internal/auth"
	"tomtly/internal/automation"
	"tomtly/internal/extension"
	"tomtly/internal/server"
	"tomtly/internal/store"
	"tomtly/plugin"
)

// config is the home's config.json.
type config struct {
	Timezone string `json:"timezone"`
	// Gone from this file. Set only to explain that at startup, rather than quietly losing every device.
	Adapters   json.RawMessage     `json:"adapters"`
	Plugins    json.RawMessage     `json:"plugins"`
	Extensions json.RawMessage     `json:"extensions"`
	Rooms      map[string][]string `json:"rooms"`
	// RoomOrder is the rooms in the order the file lists them, which a map loses.
	RoomOrder []string `json:"-"`
	// Host names allowed to reach the API, besides localhost and IP addresses. Needed behind a reverse proxy.
	AllowedHosts []string `json:"allowedHosts"`
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// loadConfig reads config.json. It holds no secrets: extensions and their settings are added on the page and kept in
// the database.
func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(cfg.Adapters) > 0 || len(cfg.Plugins) > 0 {
		return nil, fmt.Errorf(`%s: "adapters" and "plugins" are gone. Integrations and plugins are extensions now: see docs/extensions.md`, path)
	}
	if len(cfg.Extensions) > 0 {
		return nil, fmt.Errorf(`%s: "extensions" is gone. Add each one on the Integrations or Plugins page, which keeps its settings in the database, then remove the list`, path)
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}
	cfg.RoomOrder = objectKeys(data, "rooms")
	return &cfg, nil
}

// openStore retries for two minutes, because Postgres may still be starting, then gives up so Docker restarts us.
// The URL holds the password, so neither it nor Open's errors are logged with it.
func openStore(ctx context.Context, databaseURL string) (*store.Postgres, error) {
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is not set; it is the Postgres connection URL the service keeps its state in")
	}
	key, err := hex.DecodeString(os.Getenv("TOMTLY_SECRET_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("TOMTLY_SECRET_KEY must be 64 hex characters, like openssl rand -hex 32 makes; it encrypts extension settings, so keep it with your backups")
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
		st, err := store.Open(attempt, databaseURL, key)
		cancel()
		if err == nil || errors.Is(err, store.ErrWrongKey) {
			return st, err
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return nil, fmt.Errorf("database not reachable after 2 minutes: %w", err)
		}
		slog.Warn("waiting for the database", "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

// httpServer is the listener's limits. ReadTimeout is for reading a request and doesn't touch a handler that runs longer, so
// it only has to fit the biggest body: 16 MiB at about 0.5 Mbit/s takes under 5 minutes. No WriteTimeout, since inspecting
// from GitHub takes up to 2 minutes and that would cut it off.
func httpServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	configDir := env("CONFIG_DIR", "examples")
	cfg, err := loadConfig(filepath.Join(configDir, "config.json"))
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	time.Local = location // scripts' Date uses this, and a minimal image would otherwise be UTC
	st, err := openStore(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer st.Close()
	plugins := plugin.NewHub()
	extensions, err := extension.Start(ctx, configDir, env("DATA_DIR", "data"), st, plugins)
	if err != nil {
		return err
	}
	if err := extensions.SetMarketplace(os.Getenv("MARKETPLACE_URL"), os.Getenv("MARKETPLACE_PUBLIC_KEY"), os.Getenv("MARKETPLACE_APPROVAL_PUBLIC_KEYS")); err != nil {
		slog.Warn("the marketplace is not set up", "err", err)
	}
	hub := &server.Hub{Adapters: []device.Adapter{extensions}}
	engine := automation.New(hub, filepath.Join(configDir, "automations"), env("SDK_DIR", "sdk"), st, location)
	engine.Plugins = plugins
	go engine.Run(ctx.Done())

	proxies, err := auth.ParseProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	srv := httpServer(env("LISTEN", ":8099"), server.New(hub, engine, extensions, st, env("WEB_DIR", "web/dist"), cfg.Rooms, cfg.AllowedHosts, proxies, server.RoomOrder(cfg.RoomOrder)))
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			slog.Warn("shutdown", "err", err)
		}
	}()
	slog.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// resetAdmin is the way back in for a locked-out owner:
//
//	docker compose exec tomtly /tomtly reset-admin <username>
//
// It needs a shell on the machine and DATABASE_URL, which the container already has. It sets a new random password for
// the existing user, makes them an enabled admin, ends all their sessions, deletes their API tokens, and prints the password once to stdout.
// A running service still holds its in-memory login back-off, which lasts at most 15 minutes.
func resetAdmin(username string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openStore(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer st.Close()
	user, err := st.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("there is no user %q; if nobody has set up yet, open the page and create the first admin", username)
	}
	if err != nil {
		return err
	}
	password, err := auth.GeneratePassword()
	if err != nil {
		return err
	}
	hash, err := auth.Hash(password)
	if err != nil {
		return err
	}
	admin, enabled := "admin", false
	if _, err := st.UpdateUser(ctx, user.ID, store.UserChange{Role: &admin, Disabled: &enabled, Hash: &hash, DropTokens: true}); err != nil {
		return err
	}
	if err := st.AppendAudit(ctx, store.AuditEntry{Time: time.Now(), User: user.Username, Action: "admin.reset", Detail: "from the command line"}); err != nil {
		slog.Warn("audit write failed", "err", err)
	}
	fmt.Fprintf(stderr, "New password for %s (shown once, change it after logging in):\n", user.Username)
	fmt.Fprintln(stdout, password)
	return nil
}

// healthcheck asks the running service whether it answers, for Docker's HEALTHCHECK: the image has no shell or curl.
func healthcheck() error {
	_, port, err := net.SplitHostPort(env("LISTEN", ":8099"))
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", resp.Status)
	}
	return nil
}

// cli runs the subcommands and says whether to start the service instead (no arguments).
func cli(args []string, stdout, stderr io.Writer) (code int, serve bool) {
	if len(args) == 1 && args[0] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(stderr, "healthcheck:", err)
			return 1, false
		}
		return 0, false
	}
	if len(args) > 0 {
		if len(args) != 2 || args[0] != "reset-admin" {
			fmt.Fprintln(stderr, "usage: tomtly [reset-admin <username> | healthcheck]")
			return 2, false
		}
		if err := resetAdmin(args[1], stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "reset-admin:", err)
			return 1, false
		}
		return 0, false
	}
	return 0, true
}

func main() {
	code, serve := cli(os.Args[1:], os.Stdout, os.Stderr)
	if serve {
		if err := run(); err != nil {
			slog.Error("fatal", "err", err)
			code = 1
		}
	}
	if code != 0 {
		os.Exit(code)
	}
}

// objectKeys is the keys of one object in a JSON object, in the order they are written. It is nil if there is none.
func objectKeys(data []byte, field string) []string {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil || top[field] == nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(top[field]))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		keys = append(keys, t.(string))
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}
