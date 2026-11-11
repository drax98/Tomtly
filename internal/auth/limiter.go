package auth

import (
	"sync"
	"time"
)

const (
	freeFailures = 5
	maxLock      = 15 * time.Minute
	forgetAfter  = time.Hour
	maxEntries   = 10000
)

// Limiter backs off repeated login failures per key (a username, an address). It lives in memory, so a restart
// forgets it; that is acceptable because every attempt still costs a full password hash.
// A stranger can lock a known username for up to 15 minutes by failing against it. That is the price of a per-user limit.
type Limiter struct {
	mu    sync.Mutex
	Now   func() time.Time
	fails map[string]*failure
}

type failure struct {
	count int
	last  time.Time
}

func NewLimiter() *Limiter { return &Limiter{Now: time.Now, fails: map[string]*failure{}} }

func lockFor(count int) time.Duration {
	if count < freeFailures {
		return 0
	}
	return min(maxLock, 30*time.Second<<min(count-freeFailures, 10))
}

// Wait returns how long until every key may try again; zero means go ahead.
func (l *Limiter) Wait(keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wait(keys)
}

func (l *Limiter) wait(keys []string) time.Duration {
	now := l.Now()
	var wait time.Duration
	for _, k := range keys {
		if f := l.fails[k]; f != nil {
			wait = max(wait, f.last.Add(lockFor(f.count)).Sub(now))
		}
	}
	return wait
}

// Reserve counts an attempt against count before it is checked, unless a key in block is locked, in which case it says how
// long for and counts nothing. Doing both at once is what stops parallel attempts all passing the check before any of them fails.
func (l *Limiter) Reserve(block []string, count ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait := l.wait(block); wait > 0 {
		return wait
	}
	l.fail(count)
	return 0
}

// Refund takes back a reserved attempt that turned out not to be a failure.
func (l *Limiter) Refund(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if f := l.fails[k]; f != nil {
			if f.count--; f.count <= 0 {
				delete(l.fails, k)
			}
		}
	}
}

// fail counts a failed attempt against every key. The caller holds mu.
func (l *Limiter) fail(keys []string) {
	now := l.Now()
	if len(l.fails) > maxEntries {
		for k, f := range l.fails {
			if now.Sub(f.last) > forgetAfter {
				delete(l.fails, k)
			}
		}
	}
	for _, k := range keys {
		f := l.fails[k]
		if f == nil || now.Sub(f.last) > forgetAfter {
			f = &failure{}
			l.fails[k] = f
		}
		f.count++
		f.last = now
	}
}

// Clear forgets a key after a success.
func (l *Limiter) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
