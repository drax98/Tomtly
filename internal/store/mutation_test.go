package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// contractEdges is what Memory and Postgres must both do at the edges the main contract steps over: the very first
// user and the exact moment a session dies.
func contractEdges(t *testing.T, s Store) {
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	at := func(sec int) time.Time { return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC) }

	// The first user is found by id, and can hold a session and a token.
	must(s.CreateFirstUser(ctx, User{ID: "u0", Username: "first", Hash: "h", Role: "admin", Created: at(0)}))
	if got, err := s.UserByID(ctx, "u0"); err != nil || got.Username != "first" {
		t.Fatalf("UserByID(first) = %+v, %v", got, err)
	}
	must(s.CreateSession(ctx, "first-session", "u0", at(1)))
	must(s.CreateToken(ctx, Token{ID: "t0", UserID: "u0", Name: "n", Role: "viewer", Created: at(1)}, "first-token"))
	if _, owner, err := s.TokenUser(ctx, "first-token", at(2)); err != nil || owner.ID != "u0" {
		t.Fatalf("TokenUser = %+v, %v", owner, err)
	}

	// Absolute lifetime ends at exactly the limit, however recently the session was used.
	idle, absolute := 100*time.Hour, time.Hour
	must(s.CreateSession(ctx, "abs", "u0", at(1000)))
	if _, err := s.SessionUser(ctx, "abs", at(1000).Add(absolute-time.Second), idle, absolute); err != nil {
		t.Fatalf("a session just inside its lifetime: %v", err)
	}
	if _, err := s.SessionUser(ctx, "abs", at(1000).Add(absolute), idle, absolute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a session at exactly its lifetime: %v", err)
	}
	// The same cleanup keeps the living and removes the dead, at the same edge.
	must(s.CreateSession(ctx, "live", "u0", at(2000)))
	must(s.CreateSession(ctx, "edge", "u0", at(2000)))
	must(s.DeleteExpiredSessions(ctx, at(2000).Add(absolute-time.Second), idle, absolute))
	if _, err := s.SessionUser(ctx, "edge", at(2000).Add(absolute-time.Second), idle, absolute); err != nil {
		t.Fatalf("cleanup removed a living session: %v", err)
	}
	must(s.DeleteExpiredSessions(ctx, at(2000).Add(absolute), idle, absolute))
	if _, err := s.SessionUser(ctx, "live", at(2000).Add(absolute-time.Second), idle, absolute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cleanup kept a session at its lifetime: %v", err)
	}
}

func TestMemoryEdges(t *testing.T) { contractEdges(t, NewMemory()) }

func TestPostgresEdges(t *testing.T) {
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
	if _, err := p.pool.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}
	contractEdges(t, p)
}

// A store that is down must say so from every call, never answer as if it were empty.
func TestMemoryFailureIsReturnedEverywhere(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")
	m := NewMemory()
	m.Fail = boom
	now := time.Now()
	calls := map[string]error{}
	calls["CreateFirstUser"] = m.CreateFirstUser(ctx, User{})
	calls["CreateUser"] = m.CreateUser(ctx, User{})
	_, calls["UserByName"] = m.UserByName(ctx, "x")
	_, calls["UserByID"] = m.UserByID(ctx, "x")
	_, calls["ListUsers"] = m.ListUsers(ctx)
	_, calls["UpdateUser"] = m.UpdateUser(ctx, "x", UserChange{})
	calls["DeleteUser"] = m.DeleteUser(ctx, "x")
	calls["CreateSession"] = m.CreateSession(ctx, "h", "x", now)
	_, calls["SessionUser"] = m.SessionUser(ctx, "h", now, time.Hour, time.Hour)
	calls["DeleteSession"] = m.DeleteSession(ctx, "h")
	calls["DeleteExpiredSessions"] = m.DeleteExpiredSessions(ctx, now, time.Hour, time.Hour)
	calls["CreateToken"] = m.CreateToken(ctx, Token{}, "h")
	_, _, calls["TokenUser"] = m.TokenUser(ctx, "h", now)
	_, calls["ListTokens"] = m.ListTokens(ctx, "")
	calls["DeleteToken"] = m.DeleteToken(ctx, "t", "")
	calls["AppendAudit"] = m.AppendAudit(ctx, AuditEntry{})
	_, calls["RecentAudit"] = m.RecentAudit(ctx, 1)
	for name, err := range calls {
		if !errors.Is(err, boom) {
			t.Errorf("%s = %v, want the store's failure", name, err)
		}
	}
	m.Fail = nil
	if err := m.CreateUser(ctx, User{ID: "a", Username: "a"}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRefusesBadAddresses(t *testing.T) {
	_, err := Open(context.Background(), "postgres://user:hunter2secret@localhost:notaport/db", testKey)
	if err == nil || strings.Contains(err.Error(), "hunter2secret") {
		t.Errorf("a malformed address must be refused without quoting its password: %v", err)
	}
	if _, err := Open(context.Background(), "postgres://user:pw@127.0.0.1:1/db", testKey); err == nil {
		t.Error("an unreachable database must fail at once, not later")
	}
}
