// Package api is the outward face of the bounded version-history service.
// It validates every request before touching the store, so any rejected
// operation leaves all state (histories and version numbers) unchanged.
package api

import (
	"errors"
	"fmt"

	"ontology/store"
)

// Decidable sentinel errors, all mutually distinct.
var (
	ErrEmptyKey       = errors.New("api: key is empty")
	ErrInvalidK       = errors.New("api: retention limit K must be positive")
	ErrInvalidVersion = errors.New("api: version must be positive")
)

// API is the handle returned by New.
type API struct {
	s *store.Store
	k int
}

// New returns an API retaining at most K versions per key.
func New(K int) (*API, error) {
	if K <= 0 {
		return nil, ErrInvalidK
	}
	return &API{s: store.New(K), k: K}, nil
}

// Put appends value to key and returns the allocated version.
func (a *API) Put(key, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	return a.s.Put(key, value), nil
}

// Get returns the newest value of key; ok is false if never written.
func (a *API) Get(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	return a.s.Get(key)
}

// GetAt returns the value of version v of key if still retained.
func (a *API) GetAt(key string, v int64) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	if v <= 0 {
		return "", false, ErrInvalidVersion
	}
	val, ok := a.s.GetAt(key, v)
	return val, ok, nil
}

// Len returns the number of versions currently retained for key.
func (a *API) Len(key string) int {
	if key == "" {
		return 0
	}
	return a.s.Len(key)
}

// SelfCheck replays a built-in operation sequence on a throwaway instance
// and verifies the four invariants: replay consistency, bounded retention
// with contiguous monotonic versions, GetAt visibility, and rejection
// without side effects. It returns nil iff all hold.
func (a *API) SelfCheck() error {
	c, err := New(3)
	if err != nil {
		return err
	}
	vals := []string{"a", "b", "c", "d", "e", "f"}
	for i, v := range vals {
		got, err := c.Put("k", v)
		if err != nil || got != int64(i+1) {
			return fmt.Errorf("selfcheck: put %d", i)
		}
	}
	// Invariant 1+2: Get == replay last, retained count == K, contiguous.
	if got, ok := c.Get("k"); !ok || got != "f" || c.Len("k") != 3 {
		return errors.New("selfcheck: replay/bound")
	}
	// Invariant 3: retained window is exactly [maxV-K+1, maxV].
	for v, want := range map[int64]bool{1: false, 2: false, 3: false, 4: true, 5: true, 6: true, 7: false} {
		_, hit, err := c.GetAt("k", v)
		if err != nil || hit != want {
			return fmt.Errorf("selfcheck: visibility at v=%d", v)
		}
	}
	// Invariant 4: rejected ops change nothing.
	before := c.Len("k")
	if _, err := c.Put("", "x"); !errors.Is(err, ErrEmptyKey) {
		return errors.New("selfcheck: empty key put")
	}
	if _, _, err := c.GetAt("k", 0); !errors.Is(err, ErrInvalidVersion) {
		return errors.New("selfcheck: bad version")
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidK) {
		return errors.New("selfcheck: bad K")
	}
	if c.Len("k") != before {
		return errors.New("selfcheck: rejection mutated state")
	}
	if v, err := c.Put("k", "g"); err != nil || v != 7 {
		return errors.New("selfcheck: version reuse after rejection")
	}
	return nil
}
