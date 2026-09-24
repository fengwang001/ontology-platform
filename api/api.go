// Package api is the public facade over src + lcache; one mutex serializes all.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/lcache"
	"ontology/src"
)

var ErrEmptyKey = src.ErrEmptyKey
var ErrNotExist = src.ErrNotExist
var ErrTooManyKeys = lcache.ErrTooManyKeys
var ErrBadToken = errors.New("api: unknown or already-used token")

type System struct {
	mu     sync.Mutex
	s      *src.Source
	c      *lcache.Cache
	tokens map[int64]src.Token
}

func (a *System) lock() func() { a.mu.Lock(); return a.mu.Unlock }
func New(maxKeys int) *System {
	return &System{s: src.New(), c: lcache.New(maxKeys), tokens: map[int64]src.Token{}}
}

func (a *System) Update(key, val string) error { defer a.lock()(); return a.s.Update(key, val) }
func (a *System) Delete(key string) error      { defer a.lock()(); return a.s.Delete(key) }

func (a *System) Deliver(n int) error {
	defer a.lock()()
	evs := a.s.Peek(n)
	if a.c.WouldExceed(evs) {
		return ErrTooManyKeys
	}
	a.s.Drop(len(evs))
	for _, ev := range evs {
		a.c.Apply(ev)
	}
	return nil
}

func (a *System) BeginRead(key string) (t src.Token, err error) {
	defer a.lock()()
	if t, err = a.s.BeginRead(key); err == nil {
		a.tokens[t.ID] = t
	}
	return
}

// FinishRead backfills a token; (false, nil) = stale rejection, error = token survives.
func (a *System) FinishRead(t src.Token) (bool, error) {
	defer a.lock()()
	stored, ok := a.tokens[t.ID]
	if !ok {
		return false, ErrBadToken
	}
	accepted, err := a.c.Backfill(stored)
	if err != nil {
		return false, err // token survives errors
	}
	delete(a.tokens, t.ID)
	return accepted, nil
}

// Get returns (value, exists); on a miss it reads the source and backfills.
func (a *System) Get(key string) (string, bool, error) {
	defer a.lock()()
	if key == "" {
		return "", false, ErrEmptyKey
	}
	if v, exists, hit := a.c.Lookup(key); hit {
		return v, exists, nil
	}
	if !a.c.Tracked(key) && a.c.Full() {
		return "", false, ErrTooManyKeys
	}
	t, _ := a.s.BeginRead(key) // key non-empty: cannot fail
	_, _ = a.c.Backfill(t)     // budget pre-checked above: cannot fail
	return t.Val, t.Exists, nil
}
func (a *System) SourceReads() int       { defer a.lock()(); return a.s.Reads() }
func (a *System) Pending() int           { defer a.lock()(); return a.s.Pending() }
func (a *System) Fence(key string) int64 { defer a.lock()(); return a.c.Fence(key) }
func (a *System) Snapshot(key string) (string, int64, bool, bool) {
	defer a.lock()()
	return a.c.Snapshot(key)
}
func (a *System) Stats() (int, int) { defer a.lock()(); return a.c.Stats() }
func (a *System) SourceState(key string) (string, bool) {
	defer a.lock()()
	return a.s.State(key)
}

func (a *System) SelfCheck() error {
	var errs []error
	for seed := int64(0); seed < 20; seed++ {
		errs = append(errs, selfCheckOnce(seed))
	}
	return errors.Join(errs...)
}
func selfCheckOnce(seed int64) error {
	a := New(8)
	rng := rand.New(rand.NewSource(seed))
	ref := map[string]string{}
	keys := []string{"a", "b", "c", "d", "e"}
	var tokens []src.Token
	for i := 0; i < 200; i++ {
		k := keys[rng.Intn(5)]
		switch rng.Intn(6) {
		case 0:
			v := fmt.Sprint(i)
			_ = a.Update(k, v)
			ref[k] = v
		case 1:
			if a.Delete(k) == nil {
				delete(ref, k)
			}
		case 2:
			_ = a.Deliver(rng.Intn(4))
		case 3:
			t, _ := a.BeginRead(k)
			tokens = append(tokens, t)
		case 4:
			if len(tokens) > 0 {
				_, _ = a.FinishRead(tokens[0])
				tokens = tokens[1:]
			}
		default:
			_, _, _ = a.Get(k)
		}
	}
	for a.Pending() > 0 {
		_ = a.Deliver(a.Pending())
	}
	for _, tk := range tokens {
		_, _ = a.FinishRead(tk)
	}
	for _, k := range keys {
		got, exists, _ := a.Get(k)
		if want, ok := ref[k]; got != want || exists != ok {
			return fmt.Errorf("key %s: got (%q,%v), want (%q,%v)", k, got, exists, want, ok)
		}
	}
	return a.c.CheckConsistent()
}
