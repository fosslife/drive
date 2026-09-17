package auth

import (
	"sync"
	"time"
)

// Defaults for failed-login throttling. Five wrong guesses in a quarter of an
// hour is well past a typo and nowhere near a password being found.
const (
	DefaultMaxFailures   = 5
	DefaultFailureWindow = 15 * time.Minute
)

// Limiter refuses authentication attempts after too many recent failures. It is
// keyed by whatever the caller passes — username and source address — so
// spreading guesses across accounts and spreading them across one account both
// run into it.
//
// ponytail: in-memory, so it resets on restart and does not cross processes.
// That is the right size for one binary on one machine; if it ever needs to
// survive a restart the counters move into the index.
type Limiter struct {
	max    int
	window time.Duration

	mu    sync.Mutex
	fails map[string]*failures
}

type failures struct {
	count int
	until time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, fails: map[string]*failures{}}
}

// Allow reports whether an attempt may proceed. Every key must be under the
// threshold: one exhausted key refuses the attempt.
func (l *Limiter) Allow(keys ...string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if f, ok := l.fails[k]; ok && f.count >= l.max && now.Before(f.until) {
			return false
		}
	}
	return true
}

// Fail records one failed attempt against every key.
func (l *Limiter) Fail(keys ...string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	for _, k := range keys {
		f, ok := l.fails[k]
		if !ok || !now.Before(f.until) {
			f = &failures{}
			l.fails[k] = f
		}
		f.count++
		f.until = now.Add(l.window)
	}
}

// Succeed clears the counters for keys, so one correct password ends the
// penalty a user's own typos earned.
func (l *Limiter) Succeed(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.fails, k)
	}
}

// prune drops expired counters. Called on failure only, which is the only path
// that grows the map, so an idle instance holds nothing.
func (l *Limiter) prune(now time.Time) {
	for k, f := range l.fails {
		if !now.Before(f.until) {
			delete(l.fails, k)
		}
	}
}
