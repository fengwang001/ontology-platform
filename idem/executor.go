package idem

import (
	"errors"
	"sync"
	"time"
)

// entry is the per-key record. While fn is running, done is open and
// completed is false; concurrent callers block on done. Once fn finishes,
// the outcome is stored, completed is set, and done is closed.
type entry struct {
	fp        string
	done      chan struct{}
	completed bool
	res       Result
	err       error
	expires   time.Time // zero means never expires
}

// Executor runs functions under idempotency keys. It is safe for concurrent
// use. Locking is per lookup only; a slow fn under one key never blocks
// calls for other keys.
type Executor struct {
	now     func() time.Time
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]*entry
}

// New creates an Executor. now is the injectable clock used for all TTL
// decisions; it must not be nil. ttl <= 0 means records never expire.
func New(now func() time.Time, ttl time.Duration) *Executor {
	if now == nil {
		panic("idem: nil clock")
	}
	return &Executor{
		now:     now,
		ttl:     ttl,
		entries: make(map[string]*entry),
	}
}

// Do executes fn under key, or replays / awaits the existing outcome.
//
// A stored record whose fingerprint differs from fp yields
// ErrFingerprintMismatch and leaves the record untouched. A record whose
// completion time plus ttl is not after now is discarded and fn re-runs.
// Panics in fn are recovered, returned as an error to this caller only, and
// never cached.
func (e *Executor) Do(key, fp string, fn func() (Result, error)) (Result, Outcome, error) {
	for {
		res, outcome, err, retry := e.doOnce(key, fp, fn)
		if !retry {
			return res, outcome, err
		}
	}
}

// doOnce performs one attempt; retry=true means the slot we waited on was
// released without a cacheable outcome (retriable error or panic), so the
// caller should loop and try to become the new leader.
func (e *Executor) doOnce(key, fp string, fn func() (Result, error)) (Result, Outcome, error, bool) {
	e.mu.Lock()
	ent, ok := e.entries[key]
	if ok {
		if ent.fp != fp {
			e.mu.Unlock()
			return Result{}, Executed, ErrFingerprintMismatch, false
		}
		if !ent.completed {
			done := ent.done
			e.mu.Unlock()
			<-done
			e.mu.Lock()
			cur, still := e.entries[key]
			e.mu.Unlock()
			if still && cur == ent && ent.completed {
				return ent.res, Waited, ent.err, false
			}
			return Result{}, Executed, nil, true
		}
		if e.expired(ent) {
			delete(e.entries, key)
			e.mu.Unlock()
			return e.lead(key, fp, fn)
		}
		res, err := ent.res, ent.err
		e.mu.Unlock()
		return res, Replayed, err, false
	}
	e.mu.Unlock()
	return e.lead(key, fp, fn)
}

// lead claims the slot for key and runs fn as the leader.
func (e *Executor) lead(key, fp string, fn func() (Result, error)) (Result, Outcome, error, bool) {
	ent := &entry{fp: fp, done: make(chan struct{})}
	e.mu.Lock()
	if cur, ok := e.entries[key]; ok && !cur.completed {
		// Another leader claimed the slot while we were unlocked.
		e.mu.Unlock()
		<-cur.done
		e.mu.Lock()
		still, ok2 := e.entries[key]
		e.mu.Unlock()
		if ok2 && still == cur && cur.completed {
			return cur.res, Waited, cur.err, false
		}
		return Result{}, Executed, nil, true
	}
	e.entries[key] = ent
	e.mu.Unlock()

	res, err := run(fn)

	e.mu.Lock()
	if err != nil && (isRetriable(err) || isPanic(err)) {
		delete(e.entries, key)
		e.mu.Unlock()
		close(ent.done)
		return res, Executed, err, false
	}
	ent.res, ent.err = res, err
	ent.completed = true
	if e.ttl > 0 {
		ent.expires = e.now().Add(e.ttl)
	}
	e.mu.Unlock()
	close(ent.done)
	return res, Executed, err, false
}

// expired reports whether a completed record is past its TTL. Expiry is
// half-open: now < expires replays, now >= expires discards. In-flight
// records are never expired because expiry is only checked on completed
// entries.
func (e *Executor) expired(ent *entry) bool {
	if e.ttl <= 0 {
		return false
	}
	return !e.now().Before(ent.expires)
}

// run invokes fn and converts a panic into an error.
func run(fn func() (Result, error)) (res Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			res = Result{}
			err = panicError{value: v}
		}
	}()
	return fn()
}

func isPanic(err error) bool {
	var p panicError
	return errors.As(err, &p)
}
