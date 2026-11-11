package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

var testKey = bytes.Repeat([]byte{7}, 32)

func TestSealedSettingsNeedTheKeyAndTheirSource(t *testing.T) {
	if _, err := newSealer(make([]byte, 16)); err == nil {
		t.Error("a 16-byte key was taken")
	}
	s, _ := newSealer(testKey)
	sealed, err := s.seal("./a", map[string]any{"apiKey": "the-real-key"})
	if err != nil || bytes.Contains(sealed, []byte("the-real-key")) {
		t.Fatalf("%v, %q", err, sealed)
	}
	if got, err := s.open("./a", sealed); err != nil || got["apiKey"] != "the-real-key" {
		t.Errorf("round trip: %v %v", got, err)
	}
	other, _ := newSealer(bytes.Repeat([]byte{8}, 32))
	for name, open := range map[string]func() (map[string]any, error){
		"another key":    func() (map[string]any, error) { return other.open("./a", sealed) },
		"another source": func() (map[string]any, error) { return s.open("./b", sealed) },
		"cut short":      func() (map[string]any, error) { return s.open("./a", sealed[:4]) },
	} {
		if _, err := open(); !errors.Is(err, ErrWrongKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPostgresKeepsSettingsEncrypted(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	p, err := Open(ctx, url, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.pool.Exec(ctx, `TRUNCATE added_extensions`); err != nil {
		t.Fatal(err)
	}
	// A row from before encryption is sealed by the next start.
	if _, err := p.pool.Exec(ctx, `INSERT INTO added_extensions (source, settings) VALUES ('./old', '{"token":"old-plain-token"}')`); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveAddedExtension(ctx, AddedExtension{Source: "./new", Settings: map[string]any{"devices": `[{"key":"local-key-123"}]`}}); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, url, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	var raw string
	if err := p.pool.QueryRow(ctx, `SELECT string_agg(coalesce(settings::text, '') || encode(sealed, 'escape'), '|') FROM added_extensions`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"old-plain-token", "local-key-123"} {
		if bytes.Contains([]byte(raw), []byte(secret)) {
			t.Errorf("%s is in the table as written", secret)
		}
	}
	got, err := again.AddedExtensions(ctx)
	if err != nil || len(got) != 2 || got[0].Settings["token"] != "old-plain-token" || got[1].Settings["devices"] != `[{"key":"local-key-123"}]` {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Open(ctx, url, bytes.Repeat([]byte{9}, 32)); !errors.Is(err, ErrWrongKey) {
		t.Errorf("a wrong key started: %v", err)
	}
}
