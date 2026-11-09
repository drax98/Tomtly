package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryPrunesAudit(t *testing.T) {
	m := NewMemory()
	for range maxAudit + 5 {
		_ = m.AppendAudit(context.Background(), AuditEntry{Action: "a"})
	}
	if got, _ := m.RecentAudit(context.Background(), maxAudit+100); len(got) != maxAudit {
		t.Fatalf("kept %d", len(got))
	}
}

// contractAuth is the users, sessions, tokens and audit half of the contract.
func contractAuth(t *testing.T, s Store) {
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	is := func(err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	user := func(id, name, role string, sec int) User {
		return User{ID: id, Username: name, Hash: "h-" + id, Role: role, Created: at(sec)}
	}

	us, err := s.ListUsers(ctx)
	must(err)
	if len(us) != 0 {
		t.Fatalf("fresh users = %v", us)
	}
	_, err = s.UserByName(ctx, "x")
	is(err, ErrNotFound)

	// Only one of many simultaneous first users wins.
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.CreateFirstUser(ctx, user("f"+string(rune('0'+i)), "first"+string(rune('0'+i)), "admin", 0))
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else {
			is(err, ErrExists)
		}
	}
	if won != 1 {
		t.Fatalf("%d first users won", won)
	}
	us, _ = s.ListUsers(ctx)
	first := us[0]
	is(s.CreateFirstUser(ctx, user("z", "zed", "admin", 0)), ErrExists)

	must(s.CreateUser(ctx, user("b", "Bob", "viewer", 2)))
	is(s.CreateUser(ctx, user("b2", "bob", "viewer", 3)), ErrExists)
	must(s.CreateUser(ctx, user("c", "carol", "admin", 3)))
	got, err := s.UserByName(ctx, "BOB")
	must(err)
	if got.ID != "b" || got.Username != "Bob" || got.Hash != "h-b" || got.Role != "viewer" || !got.Created.Equal(at(2)) {
		t.Fatalf("UserByName = %+v", got)
	}
	if got, err = s.UserByID(ctx, "c"); err != nil || got.Username != "carol" {
		t.Fatalf("UserByID = %+v, %v", got, err)
	}
	_, err = s.UserByID(ctx, "nope")
	is(err, ErrNotFound)
	if us, _ = s.ListUsers(ctx); len(us) != 3 || us[1].ID != "b" || us[2].ID != "c" {
		t.Fatalf("ListUsers = %+v", us)
	}

	// Sessions: expiry, idle refresh, and being ended by changes.
	idle, absolute := time.Hour, 24*time.Hour
	must(s.CreateSession(ctx, "s1", "b", at(10)))
	must(s.CreateSession(ctx, "s2", "b", at(10)))
	is(s.CreateSession(ctx, "s3", "ghost", at(10)), ErrNotFound)
	if got, err = s.SessionUser(ctx, "s1", at(20), idle, absolute); err != nil || got.ID != "b" {
		t.Fatalf("SessionUser = %+v, %v", got, err)
	}
	_, err = s.SessionUser(ctx, "unknown", at(20), idle, absolute)
	is(err, ErrNotFound)
	// Use keeps it alive past the idle limit counted from creation, but not past the absolute one.
	_, err = s.SessionUser(ctx, "s1", at(10+3000), idle, absolute)
	must(err)
	_, err = s.SessionUser(ctx, "s1", at(10+3000+3000), idle, absolute)
	must(err)
	_, err = s.SessionUser(ctx, "s2", at(10+3600), idle, absolute) // idle for exactly the limit
	is(err, ErrNotFound)
	_, err = s.SessionUser(ctx, "s1", at(10+86400), idle, absolute)
	is(err, ErrNotFound)
	must(s.CreateSession(ctx, "s4", "b", at(100)))
	must(s.DeleteExpiredSessions(ctx, at(100+7200), idle, absolute))
	_, err = s.SessionUser(ctx, "s4", at(100+7201), idle, absolute)
	is(err, ErrNotFound)
	must(s.CreateSession(ctx, "s5", "b", at(500)))
	must(s.DeleteSession(ctx, "s5"))
	must(s.DeleteSession(ctx, "s5"))
	_, err = s.SessionUser(ctx, "s5", at(501), idle, absolute)
	is(err, ErrNotFound)

	// An update ends the user's sessions except the kept one, and returns the new state.
	must(s.CreateSession(ctx, "k1", "b", at(600)))
	must(s.CreateSession(ctx, "k2", "b", at(600)))
	must(s.CreateSession(ctx, "other", "c", at(600)))
	role, hash := "operator", "newhash"
	updated, err := s.UpdateUser(ctx, "b", UserChange{Role: &role, Hash: &hash, KeepSession: "k1"})
	must(err)
	if updated.Role != "operator" || updated.Hash != "newhash" || updated.Username != "Bob" {
		t.Fatalf("UpdateUser = %+v", updated)
	}
	if _, err = s.SessionUser(ctx, "k1", at(601), idle, absolute); err != nil {
		t.Fatalf("kept session lost: %v", err)
	}
	_, err = s.SessionUser(ctx, "k2", at(601), idle, absolute)
	is(err, ErrNotFound)
	if _, err = s.SessionUser(ctx, "other", at(601), idle, absolute); err != nil {
		t.Fatalf("another user's session lost: %v", err)
	}
	if got, _ = s.UserByID(ctx, "b"); got.Role != "operator" || got.Hash != "newhash" {
		t.Fatalf("stored %+v", got)
	}
	_, err = s.UpdateUser(ctx, "nope", UserChange{Role: &role})
	is(err, ErrNotFound)
	if _, err = s.UpdateUser(ctx, "b", UserChange{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SessionUser(ctx, "k1", at(602), idle, absolute); err != nil {
		t.Fatalf("an empty change ended a session: %v", err)
	}

	// Last admin: two admins, both demoted at once, exactly one may succeed.
	viewer, yes := "viewer", true
	errs := make(chan error, 2)
	for _, id := range []string{first.ID, "c"} {
		go func() { _, err := s.UpdateUser(ctx, id, UserChange{Role: &viewer}); errs <- err }()
	}
	var ok, last int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrLastAdmin):
			last++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || last != 1 {
		t.Fatalf("concurrent demotes: %d succeeded, %d refused", ok, last)
	}
	us, _ = s.ListUsers(ctx)
	admin := slices.IndexFunc(us, func(u User) bool { return u.Role == "admin" })
	if admin < 0 {
		t.Fatal("no admin left")
	}
	adminID := us[admin].ID
	_, err = s.UpdateUser(ctx, adminID, UserChange{Disabled: &yes})
	is(err, ErrLastAdmin)
	is(s.DeleteUser(ctx, adminID), ErrLastAdmin)
	// Another admin makes the first one removable; a disabled admin doesn't count as one.
	must(s.CreateUser(ctx, user("d", "dave", "admin", 5)))
	if _, err = s.UpdateUser(ctx, "d", UserChange{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	is(s.DeleteUser(ctx, adminID), ErrLastAdmin)
	no := false
	if _, err = s.UpdateUser(ctx, "d", UserChange{Disabled: &no}); err != nil {
		t.Fatal(err)
	}

	// Tokens.
	must(s.CreateToken(ctx, Token{ID: "t1", UserID: "b", Name: "one", Role: "viewer", Created: at(700)}, "th1"))
	must(s.CreateToken(ctx, Token{ID: "t2", UserID: "d", Name: "two", Role: "admin", Created: at(701)}, "th2"))
	is(s.CreateToken(ctx, Token{ID: "t3", UserID: "ghost", Name: "x", Role: "viewer", Created: at(702)}, "th3"), ErrNotFound)
	tok, owner, err := s.TokenUser(ctx, "th1", at(800))
	must(err)
	if tok.ID != "t1" || tok.Owner != "Bob" || tok.Role != "viewer" || owner.ID != "b" || owner.Role != "operator" {
		t.Fatalf("TokenUser = %+v, %+v", tok, owner)
	}
	_, _, err = s.TokenUser(ctx, "nope", at(800))
	is(err, ErrNotFound)
	list, err := s.ListTokens(ctx, "")
	must(err)
	if len(list) != 2 || list[0].ID != "t1" || list[1].ID != "t2" || list[0].Owner != "Bob" || !list[0].LastUsed.Equal(at(800)) || !list[1].LastUsed.IsZero() {
		t.Fatalf("ListTokens = %+v", list)
	}
	if list, _ = s.ListTokens(ctx, "d"); len(list) != 1 || list[0].ID != "t2" {
		t.Fatalf("ListTokens(d) = %+v", list)
	}
	if list, _ = s.ListTokens(ctx, "c"); list == nil || len(list) != 0 {
		t.Fatalf("ListTokens(c) = %#v", list)
	}
	is(s.DeleteToken(ctx, "t1", "d"), ErrNotFound)
	must(s.DeleteToken(ctx, "t1", "b"))
	is(s.DeleteToken(ctx, "t1", ""), ErrNotFound)
	must(s.DeleteToken(ctx, "t2", ""))
	must(s.CreateToken(ctx, Token{ID: "t4", UserID: "b", Name: "four", Role: "viewer", Created: at(703)}, "th4"))

	// Deleting a user takes their sessions and tokens along.
	must(s.DeleteUser(ctx, "b"))
	is(s.DeleteUser(ctx, "b"), ErrNotFound)
	_, err = s.SessionUser(ctx, "k1", at(900), idle, absolute)
	is(err, ErrNotFound)
	_, _, err = s.TokenUser(ctx, "th4", at(900))
	is(err, ErrNotFound)
	_, err = s.UserByName(ctx, "bob")
	is(err, ErrNotFound)

	// Audit: newest first.
	must(s.AppendAudit(ctx, AuditEntry{Time: at(1), User: "u", Action: "one", Detail: "d", IP: "1.2.3.4"}))
	must(s.AppendAudit(ctx, AuditEntry{Time: at(2), User: "v", Action: "two"}))
	entries, err := s.RecentAudit(ctx, 5)
	must(err)
	if len(entries) != 2 || entries[0].Action != "two" || entries[1].Action != "one" || entries[1].IP != "1.2.3.4" || !entries[1].Time.Equal(at(1)) {
		t.Fatalf("RecentAudit = %+v", entries)
	}
	if entries, _ = s.RecentAudit(ctx, 1); len(entries) != 1 || entries[0].Action != "two" {
		t.Fatalf("RecentAudit(1) = %+v", entries)
	}
}

// A password can be changing because a token got out, so the change can take the tokens with it, all at once.
func TestUpdateUserCanDropTheirTokens(t *testing.T) {
	checks := func(t *testing.T, s Store) {
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Microsecond)
		for _, name := range []string{"one", "two"} {
			if err := s.CreateUser(ctx, User{ID: name, Username: name, Hash: "h", Role: "admin", Created: now}); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateToken(ctx, Token{ID: name + "-t", UserID: name, Name: "n", Role: "viewer", Created: now}, name+"-hash"); err != nil {
				t.Fatal(err)
			}
		}
		hash := "new"
		if _, err := s.UpdateUser(ctx, "one", UserChange{Hash: &hash}); err != nil {
			t.Fatal(err)
		}
		if tokens, _ := s.ListTokens(ctx, "one"); len(tokens) != 1 {
			t.Errorf("kept unless asked: %+v", tokens)
		}
		if _, err := s.UpdateUser(ctx, "one", UserChange{Hash: &hash, DropTokens: true}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.TokenUser(ctx, "one-hash", now); !errors.Is(err, ErrNotFound) {
			t.Errorf("the token still works: %v", err)
		}
		if tokens, _ := s.ListTokens(ctx, "two"); len(tokens) != 1 {
			t.Errorf("someone else's token went: %+v", tokens)
		}
	}
	t.Run("memory", func(t *testing.T) { checks(t, NewMemory()) })
	t.Run("postgres", func(t *testing.T) {
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			t.Skip("TEST_DATABASE_URL is not set")
		}
		p, err := Open(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if _, err := p.pool.Exec(context.Background(), `TRUNCATE users CASCADE`); err != nil {
			t.Fatal(err)
		}
		checks(t, p)
	})
}

func TestMarketStateIsKept(t *testing.T) {
	checks := func(t *testing.T, s Store) {
		ctx := context.Background()
		state, err := s.MarketState(ctx)
		if err != nil || !state.Newest.IsZero() {
			t.Fatalf("a new home has seen nothing: %+v, %v", state, err)
		}
		at := time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)
		for _, n := range []int{1, 2} {
			want := MarketState{Newest: at.Add(time.Duration(n) * time.Hour), NewestSigned: at.Add(time.Duration(n) * time.Minute), Revoked: json.RawMessage(`[{"id": "a"}]`)}
			if err := s.SaveMarketState(ctx, want); err != nil {
				t.Fatal(err)
			}
			got, err := s.MarketState(ctx)
			if err != nil || !got.Newest.Equal(want.Newest) || !got.NewestSigned.Equal(want.NewestSigned) || !strings.Contains(string(got.Revoked), `"id"`) {
				t.Errorf("got %+v, %v", got, err)
			}
		}
	}
	t.Run("memory", func(t *testing.T) { checks(t, NewMemory()) })
	t.Run("postgres", func(t *testing.T) {
		url := os.Getenv("TEST_DATABASE_URL")
		if url == "" {
			t.Skip("TEST_DATABASE_URL is not set")
		}
		p, err := Open(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if _, err := p.pool.Exec(context.Background(), `TRUNCATE market_state`); err != nil {
			t.Fatal(err)
		}
		checks(t, p)
	})
}
