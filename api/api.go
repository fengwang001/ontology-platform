// Package api is the outward-facing entry point for bounded per-key
// version history. It validates arguments, classifies every failure with
// a distinct sentinel error and delegates storage to package store.
package api

import (
	"fmt"

	"ontology/store"
)

// API is safe for concurrent use by multiple goroutines.
type API struct {
	s *store.Store
}

// New creates an API retaining at most k versions per key.
func New(k int) (*API, error) {
	s, err := store.New(k)
	if err != nil {
		return nil, err
	}
	return &API{s: s}, nil
}

// Put appends value to key and returns the newly assigned version.
func (a *API) Put(key, value string) (int64, error) {
	return a.s.Put(key, value)
}

// Get returns the latest value of key. An empty key has never been
// written, so it is reported as missing ("", false); the classifiable
// empty-key error is delivered by Put and GetAt.
func (a *API) Get(key string) (string, bool) {
	val, ok, _ := a.s.Get(key)
	return val, ok
}

// GetAt returns the value of version v of key. It hits iff v is still in
// the retained window; cleaned/future versions and unknown keys miss.
func (a *API) GetAt(key string, version int64) (string, bool, error) {
	return a.s.GetAt(key, version)
}

// Len returns the number of versions currently retained for key.
func (a *API) Len(key string) int { return a.s.Len(key) }

// SelfCheck replays a built-in operation sequence on fresh internal
// instances and verifies the four invariants: replay consistency,
// bounded/latest window, GetAt visibility, and no-trace rejection. It
// never mutates the receiver, so concurrent SelfCheck calls are safe.
// It returns nil when all hold.
func (a *API) SelfCheck() error {
	// Invariant 2+3: the six-Put table of the spec, on a fresh K=3 instance.
	t, err := New(3)
	if err != nil {
		return err
	}
	wantKeep := [][]int64{{1}, {1, 2}, {1, 2, 3}, {2, 3, 4}, {3, 4, 5}, {4, 5, 6}}
	evicted := []int64{0, 0, 0, 1, 2, 3}
	for i := 0; i < 6; i++ {
		v, err := t.Put("chk", string(rune('a'+i)))
		if err != nil || v != int64(i+1) {
			return fmt.Errorf("selfcheck: put %d -> v=%d err=%v", i+1, v, err)
		}
		got, err := t.s.Retained("chk")
		if err != nil || len(got) != len(wantKeep[i]) {
			return fmt.Errorf("selfcheck: step %d retained=%v, want %v", i+1, got, wantKeep[i])
		}
		for j := range got {
			if got[j] != wantKeep[i][j] {
				return fmt.Errorf("selfcheck: step %d retained=%v, want %v", i+1, got, wantKeep[i])
			}
		}
		if i == 2 && len(got) != 3 { // 甲: count == K does NOT trigger cleanup
			return fmt.Errorf("selfcheck: step 3 over-eager cleanup, retained %v", got)
		}
		if evicted[i] != 0 {
			if _, hit, _ := t.GetAt("chk", evicted[i]); hit {
				return fmt.Errorf("selfcheck: evicted v%d still readable", evicted[i])
			}
		}
		if i == 3 { // 乙+丙, checked right after step 4 (Put "d")
			// 乙: oldest retained version is now 2 and GetAt(2) hits.
			if got, hit, _ := t.GetAt("chk", 2); !hit || got != "b" {
				return fmt.Errorf("selfcheck: step4 GetAt(2)=%q,%v want b,true", got, hit)
			}
			// 丙: v1 was physically cleaned, so GetAt(1) must miss.
			if _, hit, _ := t.GetAt("chk", 1); hit {
				return fmt.Errorf("selfcheck: step4 cleaned v1 still readable")
			}
		}
	}
	// Invariant 1: Get equals the last Put of a full replay, many keys,
	// on a fresh instance using the receiver's own K.
	k := a.s.K()
	r, err := New(k)
	if err != nil {
		return err
	}
	last := map[string]string{}
	for i := 1; i <= 200; i++ {
		key := fmt.Sprintf("k%d", i%4)
		val := fmt.Sprintf("%s@%d", key, i)
		if _, err := r.Put(key, val); err != nil {
			return err
		}
		last[key] = val
		if r.Len(key) > k {
			return fmt.Errorf("selfcheck: key %s retains %d > K", key, r.Len(key))
		}
		if got, ok := r.Get(key); !ok || got != last[key] {
			return fmt.Errorf("selfcheck: Get %q=%q,%v diverges from replay %q", key, got, ok, last[key])
		}
	}
	// Invariant 4: rejected ops change nothing.
	before := r.Len("k1")
	if _, err := r.Put("", "x"); err != store.ErrEmptyKey {
		return fmt.Errorf("selfcheck: empty put err=%v", err)
	}
	if _, _, err := r.GetAt("", 1); err != store.ErrEmptyKey {
		return fmt.Errorf("selfcheck: empty getat err=%v", err)
	}
	if _, _, err := r.GetAt("k1", 0); err != store.ErrInvalidVersion {
		return fmt.Errorf("selfcheck: bad version err=%v", err)
	}
	if r.Len("k1") != before {
		return fmt.Errorf("selfcheck: rejected op mutated state")
	}
	// Missing keys and future versions miss cleanly.
	if _, ok := r.Get("no-such-key"); ok {
		return fmt.Errorf("selfcheck: unknown key reported present")
	}
	if _, hit, _ := r.GetAt("k1", 999999); hit {
		return fmt.Errorf("selfcheck: future version reported hit")
	}
	return nil
}
