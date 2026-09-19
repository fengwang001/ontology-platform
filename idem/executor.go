package idem

import (
	"fmt"
	"sync"
	"time"
)

// entry is the per-key record. While done is false the key is in-flight and
// ch is open; completing or releasing the entry closes ch to wake waiters.
type entry struct {
	fp      string
	done    bool
	res     Result
	err     error
	expires time.Time // zero means never expires
	ch      chan struct{}
}

// Executor runs functions at most once per idempotency key.
type Executor struct {
	now func() time.Time
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]*entry
}

// New creates an Executor. now is the injectable clock; ttl is measured from
// the moment a record completes, and ttl <= 0 means records never expire.
func New(now func() time.Time, ttl time.Duration) *Executor {
	return &Executor{now: now, ttl: ttl, m: make(map[string]*entry)}
}

// Do runs fn for key unless a matching record already exists. Calls with the
// same key and fingerprint replay or wait for the first call's result; a
// different fingerprint yields ErrFingerprintMismatch.
func (e *Executor) Do(key, fp string, fn func() (Result, error)) (Result, Outcome, error) {
	for {
		e.mu.Lock()
		ent, ok := e.m[key]
		if ok && ent.done && e.expired(ent) {
			delete(e.m, key)
			ok = false
		}
		if ok {
			if ent.fp != fp {
				e.mu.Unlock()
				return Result{}, Executed, ErrFingerprintMismatch
			}
			if ent.done {
				res, err := ent.res, ent.err
				e.mu.Unlock()
				return res, Replayed, err
			}
			ch := ent.ch
			e.mu.Unlock()
			<-ch
			if res, err, ok := ent.result(); ok {
				return res, Waited, err
			}
			continue // slot was released or expired; retry from scratch
		}
		ent = &entry{fp: fp, ch: make(chan struct{})}
		e.m[key] = ent
		e.mu.Unlock()
		return e.execute(key, ent, fn)
	}
}

// expired reports whether a completed record has reached its expiry instant
// (completion + ttl, left-closed right-open).
func (e *Executor) expired(ent *entry) bool {
	if ent.expires.IsZero() {
		return false
	}
	return !e.now().Before(ent.expires)
}

// result returns the completed record's values, or ok=false if the entry was
// released without caching (retriable failure or panic).
func (ent *entry) result() (Result, error, bool) {
	if !ent.done {
		return Result{}, nil, false
	}
	return ent.res, ent.err, true
}

// execute runs fn and settles the entry: caching ordinary outcomes and
// releasing the slot on retriable failures or panics.
func (e *Executor) execute(key string, ent *entry, fn func() (Result, error)) (res Result, oc Outcome, err error) {
	defer func() {
		if r := recover(); r != nil {
			e.release(key, ent)
			res, oc, err = Result{}, Executed, fmt.Errorf("idem: panic: %v", r)
		}
	}()
	res, err = fn()
	if isRetriable(err) {
		e.release(key, ent)
		return res, Executed, err
	}
	e.complete(ent, res, err)
	return res, Executed, err
}

// complete stores the outcome, stamps the expiry from the injected clock,
// and wakes any waiters.
func (e *Executor) complete(ent *entry, res Result, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent.res = res
	ent.err = err
	ent.done = true
	if e.ttl > 0 {
		ent.expires = e.now().Add(e.ttl)
	}
	close(ent.ch)
}

// release drops the in-flight entry without caching and wakes waiters so
// they can retry.
func (e *Executor) release(key string, ent *entry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.m[key] == ent {
		delete(e.m, key)
	}
	close(ent.ch)
}
