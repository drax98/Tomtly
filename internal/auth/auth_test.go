package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func init() { Params.Memory, Params.Time = 64, 1 }

func TestPasswordHash(t *testing.T) {
	a, _ := Hash("correct horse battery")
	b, _ := Hash("correct horse battery")
	if a == b {
		t.Error("two hashes of one password share a salt")
	}
	if !strings.HasPrefix(a, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("format: %s", a)
	}
	if !verifies(a, "correct horse battery") || verifies(a, "correct horse batterY") || verifies(a, "") {
		t.Error("verify")
	}
	// Old hashes keep working after the cost changes.
	Params.Time = 2
	defer func() { Params.Time = 1 }()
	if !verifies(a, "correct horse battery") {
		t.Error("a hash made with older parameters stopped verifying")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=64,t=1,p=1$AAAA$AAAA", "$argon2i$v=19$m=64,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=99999999,t=1,p=1$AAAA$AAAA"} {
		if verifies(bad, "x") {
			t.Errorf("%q verified", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for pw, ok := range map[string]bool{"": false, "elevenchars": false, "twelve chars": true, strings.Repeat("a", 256): true, strings.Repeat("a", 257): false} {
		if (ValidatePassword(pw) == nil) != ok {
			t.Errorf("%d chars: ok = %v", len(pw), !ok)
		}
	}
	if pw, err := GeneratePassword(); err != nil || ValidatePassword(pw) != nil {
		t.Errorf("generated %q: %v", pw, err)
	}
}

func TestSecrets(t *testing.T) {
	s1, h1 := NewSecret("p_")
	s2, _ := NewSecret("p_")
	if s1 == s2 || !strings.HasPrefix(s1, "p_") || len(s1) < 40 || HashSecret(s1) != h1 || strings.Contains(h1, s1) {
		t.Error(s1, h1)
	}
}

func TestRoles(t *testing.T) {
	if len(Permissions("viewer")) != 3 || len(Permissions("operator")) != 4 || len(Permissions("editor")) != 6 || len(Permissions("admin")) != 11 || len(Permissions("other")) != 0 {
		t.Error("role sizes")
	}
	if Has("editor", AutomationsApprove) || !Has("admin", AutomationsApprove) || Has("operator", AutomationsEdit) || !Has("editor", AutomationsLive) || Has("editor", UsersManage) || !Has("admin", AuditRead) || Has("editor", SettingsManage) || !Has("admin", SettingsManage) {
		t.Error("has")
	}
	if !AtMost("viewer", "operator") || AtMost("admin", "editor") || AtMost("nope", "admin") || !AtMost("admin", "admin") {
		t.Error("at most")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for range 4 {
		l.Reserve(nil, "a")
	}
	if l.Wait("a") != 0 {
		t.Error("locked before the fifth failure")
	}
	l.Reserve(nil, "a")
	if w := l.Wait("a", "b"); w != 30*time.Second {
		t.Errorf("wait = %v", w)
	}
	now = now.Add(31 * time.Second)
	if l.Wait("a") != 0 {
		t.Error("still locked")
	}
	l.Reserve(nil, "a") // the sixth doubles it
	if w := l.Wait("a"); w != time.Minute {
		t.Errorf("wait = %v", w)
	}
	l.Clear("a")
	if l.Wait("a") != 0 {
		t.Error("clear")
	}
	for range 40 {
		l.Reserve(nil, "b")
	}
	if w := l.Wait("b"); w != 15*time.Minute {
		t.Errorf("cap: %v", w)
	}
	now = now.Add(2 * time.Hour)
	l.Reserve(nil, "b") // long quiet forgets the history
	if l.Wait("b") != 0 {
		t.Error("old failures counted")
	}
}

// craft builds a PHC string with the given cost so the accepted range can be probed at its edges.
func craft(password string, memory, time uint32) string {
	salt := []byte("0123456789abcdef")
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=1$%s$%s", memory, time, b64.EncodeToString(salt), b64.EncodeToString(mustDerive(password, salt, memory, time)))
}

func TestVerifyCostBounds(t *testing.T) {
	const pw = "correct horse battery"
	for _, c := range []struct {
		memory, time uint32
		ok           bool
	}{{8, 1, true}, {8, 20, true}, {7, 1, false}, {8, 21, false}} {
		if got := verifies(craft(pw, c.memory, c.time), pw); got != c.ok {
			t.Errorf("m=%d t=%d: verified = %v, want %v", c.memory, c.time, got, c.ok)
		}
	}
	if verifies(craft(pw, 1<<20+1, 1), pw) {
		t.Error("a hash demanding over 1 GiB verified")
	}
}

func TestAtMostIncludesLowestRole(t *testing.T) {
	if !AtMost("viewer", "viewer") || !AtMost("viewer", "admin") || AtMost("operator", "viewer") {
		t.Error("the lowest role must be within its own limit and no role within a lower one")
	}
}

func TestLimiterForgetsExactlyAfterAnHour(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for range 5 {
		l.Reserve(nil, "a")
	}
	now = now.Add(time.Hour) // not yet past the hour: the history still counts
	l.Reserve(nil, "a")
	if w := l.Wait("a"); w != time.Minute {
		t.Errorf("wait = %v, want the sixth failure to double the lock", w)
	}
}

func TestLimiterPrunesOldEntriesWhenFull(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for i := range maxEntries + 1 {
		l.Reserve(nil, fmt.Sprint("old", i))
	}
	l.Reserve(nil, "recent") // 10002 entries now, all of them an hour old exactly
	now = now.Add(time.Hour)
	l.Reserve(nil, "keep") // the table is over its limit, but nothing is past the hour yet
	if len(l.fails) != maxEntries+3 {
		t.Fatalf("entries pruned too early: %d", len(l.fails))
	}
	now = now.Add(time.Second)
	l.Reserve(nil, "keep")
	if len(l.fails) != 1 {
		t.Errorf("stale entries kept: %d", len(l.fails))
	}
}

func TestLimiterNoPruneAtLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter()
	l.Now = func() time.Time { return now }
	for i := range maxEntries {
		l.Reserve(nil, fmt.Sprint("k", i))
	}
	now = now.Add(2 * time.Hour)
	l.Reserve(nil, "x") // exactly at the limit: no sweep yet
	if len(l.fails) != maxEntries+1 {
		t.Errorf("swept at the limit: %d", len(l.fails))
	}
}

func verifies(encoded, password string) bool {
	ok, err := Verify(encoded, password)
	return ok && err == nil
}

// Parallel attempts can't all pass the check before any of them fails.
func TestReserveCountsBeforeTheCheck(t *testing.T) {
	l := NewLimiter()
	var through atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Reserve([]string{"a"}, "a") == 0 {
				through.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := through.Load(); got != freeFailures {
		t.Errorf("%d attempts got through, want %d", got, freeFailures)
	}
	l.Refund("a", "a", "a", "a", "a")
	if l.Wait("a") != 0 {
		t.Error("refunded attempts still lock")
	}
	if _, ok := l.fails["a"]; ok {
		t.Error("a refunded key should be forgotten")
	}
}

func TestReserveCountsAKeyItDoesNotBlockOn(t *testing.T) {
	l := NewLimiter()
	for range freeFailures {
		l.Reserve(nil, "ip")
	}
	if l.Reserve([]string{"name"}, "name", "ip") != 0 {
		t.Error("a locked address must not stop the name's attempt")
	}
	if l.Reserve([]string{"ip"}, "ip") == 0 {
		t.Error("the address is locked")
	}
}

func TestVerifyGivesUpWhenTheQueueIsFull(t *testing.T) {
	hash, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	old := SlotWait
	SlotWait = 10 * time.Millisecond
	defer func() { SlotWait = old }()
	for range cap(slots) {
		slots <- struct{}{}
	}
	defer func() {
		for range cap(slots) {
			<-slots
		}
	}()
	if ok, err := Verify(hash, "correct horse battery"); ok || !errors.Is(err, ErrBusy) {
		t.Errorf("verify: %v, %v", ok, err)
	}
	if _, err := Hash("x"); !errors.Is(err, ErrBusy) {
		t.Errorf("hash: %v", err)
	}
}

func TestClientAddress(t *testing.T) {
	proxies, err := ParseProxies("10.0.0.1, 172.16.0.0/12,")
	if err != nil || len(proxies) != 2 || proxies[0].Bits() != 32 || proxies[1].Bits() != 12 {
		t.Fatalf("proxies: %v, %v", proxies, err)
	}
	if _, err := ParseProxies("nope"); err == nil {
		t.Error("a bad address was accepted")
	}
	for _, c := range []struct {
		name, peer string
		forwarded  []string
		want       string
	}{
		{"no proxy", "8.8.8.8:1", []string{"1.1.1.1"}, "8.8.8.8"},
		{"through a proxy", "10.0.0.1:1", []string{"1.1.1.1"}, "1.1.1.1"},
		{"a forged first entry", "10.0.0.1:1", []string{"6.6.6.6, 1.1.1.1"}, "1.1.1.1"},
		{"two proxies", "10.0.0.1:1", []string{"6.6.6.6, 1.1.1.1, 172.16.0.5"}, "1.1.1.1"},
		{"a second header line", "10.0.0.1:1", []string{"6.6.6.6", "1.1.1.1"}, "1.1.1.1"},
		{"a forged first header line", "10.0.0.1:1", []string{"6.6.6.6, 5.5.5.5", "1.1.1.1, 172.16.0.5"}, "1.1.1.1"},
		{"junk from the client", "10.0.0.1:1", []string{"junk, 1.1.1.1"}, "1.1.1.1"},
		{"junk at the end", "10.0.0.1:1", []string{"1.1.1.1, junk"}, "10.0.0.1"},
		{"nothing forwarded", "10.0.0.1:1", nil, "10.0.0.1"},
		{"only proxies", "10.0.0.1:1", []string{"172.16.0.5"}, "172.16.0.5"},
		{"a mapped address", "[::ffff:10.0.0.1]:1", []string{"1.1.1.1"}, "1.1.1.1"},
	} {
		got, ok := ClientAddress(c.peer, c.forwarded, proxies)
		if !ok || got.String() != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if _, ok := ClientAddress("not an address", nil, proxies); ok {
		t.Error("a bad peer")
	}
}

func mustDerive(password string, salt []byte, memory, cost uint32) []byte {
	key, err := derive(password, salt, memory, cost)
	if err != nil {
		panic(err)
	}
	return key
}
