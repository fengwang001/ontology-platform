package idem

import (
	"fmt"
	"sync"
	"time"
)

// entry is the per-key record. While fn is running, done is open and
// complete is false; when fn finishes, the outcome is stored, complete
// is set (unless the slot was released), and done is closed.
type entry struct {
	fp       string
	done     chan struct{}
	res      Result
	err      error
	complete bool
	expires  time.Time // zero means never expires
}

// Executor executes functions at most once per idempotency key.
type Executor struct {
	now func() time.Time
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*entry
}

// New creates an Executor. now is the injectable clock; ttl <= 0 means
// records never expire.
func New(now func() time.Time, ttl time.Duration) *Executor {
	return &Executor{now: now, ttl: ttl, entries: make(map[string]*entry)}
}

// Do runs fn for key, or replays / waits for an existing outcome.
func (e *Executor) Do(key, fp string, fn func() (Result, error)) (Result, Outcome, error) {
	waited := false
	for {
		e.mu.Lock()
		ent, ok := e.entries[key]
		if ok && ent.complete && e.expired(ent) {
			delete(e.entries, key)
			ok = false
		}
		if ok {
			if ent.fp != fp {
				e.mu.Unlock()
				return Result{}, 0, ErrFingerprintMismatch
			}
			if ent.complete {
				res, err := ent.res, ent.err
				e.mu.Unlock()
				return res, outcome(waited, Replayed), err
			}
			ch := ent.done
			e.mu.Unlock()
			<-ch
			waited = true
			continue
		}
		ent = &entry{fp: fp, done: make(chan struct{})}
		e.entries[key] = ent
		e.mu.Unlock()

		res, err := e.run(key, ent, fn)
		return res, outcome(waited, Executed), err
	}
}

func outcome(waited bool, o Outcome) Outcome {
	if waited {
		return Waited
	}
	return o
}

func (e *Executor) expired(ent *entry) bool {
	return !ent.expires.IsZero() && !e.now().Before(ent.expires)
}

// run executes fn, converts a panic into an error, and finalizes the
// entry: business outcomes are cached, retriable errors and panics
// release the slot.
func (e *Executor) run(key string, ent *entry, fn func() (Result, error)) (res Result, err error) {
	cache := false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
			err = fmt.Errorf("idem: fn panicked: %v", r)
			cache = false
		}
		e.mu.Lock()
		if cache {
			ent.res = res
			ent.err = err
			ent.complete = true
			if e.ttl > 0 {
				ent.expires = e.now().Add(e.ttl)
			}
		} else if e.entries[key] == ent {
			delete(e.entries, key)
		}
		e.mu.Unlock()
		close(ent.done)
	}()
	res, err = fn()
	cache = !isRetriable(err)
	return res, err
}
