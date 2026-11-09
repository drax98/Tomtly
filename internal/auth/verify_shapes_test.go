package auth

import (
	"strings"
	"testing"
	"time"
)

// Each part of a stored hash is checked on its own: one wrong part is enough to refuse it, whatever the others say.
func TestVerifyRefusesEveryMalformedPart(t *testing.T) {
	const pw = "correct horse battery"
	good := craft(pw, 8, 1)
	if !verifies(good, pw) {
		t.Fatal("the well-formed hash")
	}
	for name, bad := range map[string]string{
		"another algorithm": strings.Replace(good, "$argon2id$", "$argon2i$", 1),
		"another version":   strings.Replace(good, "$v=19$", "$v=18$", 1),
		"a part missing":    good[:strings.LastIndex(good, "$")],
		"two threads":       strings.Replace(good, ",p=1$", ",p=2$", 1),
		"unreadable costs":  strings.Replace(good, "$m=8,", "$m=x,", 1),
		"too much memory":   strings.Replace(good, "$m=8,", "$m=2097152,", 1),
		"a bad salt":        strings.Replace(good, "$MDEy", "$!!!!", 1),
		"a short key":       good[:len(good)-4],
	} {
		if ok, err := Verify(bad, pw); ok || err != nil {
			t.Errorf("%s: verified %v, %v", name, ok, err)
		}
	}
}

// An unknown name must cost what a wrong password costs, or the time taken says which names exist.
func TestSpendTimeDoesAFullVerification(t *testing.T) {
	saved := Params
	defer func() { Params = saved }()
	Params.Memory, Params.Time = 32*1024, 2
	dummyMu.Lock()
	dummy = "" // made again with these costs
	dummyMu.Unlock()
	if err := SpendTime("x"); err != nil { // makes the dummy hash
		t.Fatal(err)
	}
	dummyMu.Lock()
	made := dummy
	dummyMu.Unlock()
	if !verifies(made, "not a real password") {
		t.Fatal("the dummy isn't a real hash")
	}
	// The fastest of a few tries each, so a busy machine (other packages' tests run at the same time) doesn't decide it.
	fastest := func(run func()) time.Duration {
		best := time.Hour
		for range 5 {
			start := time.Now()
			run()
			best = min(best, time.Since(start))
		}
		return best
	}
	verify := fastest(func() { Verify(made, "wrong") })
	spent := fastest(func() {
		if err := SpendTime("wrong"); err != nil {
			t.Fatal(err)
		}
	})
	if spent < verify/3 {
		t.Errorf("an unknown name took %v, a wrong password %v", spent, verify)
	}
}

func TestRefundKeepsTheOtherFailures(t *testing.T) {
	l := NewLimiter()
	l.Reserve([]string{"k"}, "k")
	l.Reserve([]string{"k"}, "k")
	l.Refund("k")
	if f := l.fails["k"]; f == nil || f.count != 1 {
		t.Fatalf("after two failures and a refund: %+v", f)
	}
	l.Refund("k")
	if _, ok := l.fails["k"]; ok {
		t.Error("a key with no failures left is kept")
	}
}
