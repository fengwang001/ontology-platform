// Package api is the concurrency-safe front end for the two-level LWW-Map
// CRDT. Dependency direction is api -> omap -> lww; reverse deps impossible.
package api

import (
	"fmt"
	"reflect"
	"sync"

	"ontology/omap"
)

type Op = omap.Op

const (
	Put      = omap.Put
	DelOuter = omap.DelOuter
)

// Distinct decidable sentinel errors, re-exported for errors.Is callers.
var (
	ErrEmptyOuterKey = omap.ErrEmptyOuterKey
	ErrEmptyInnerKey = omap.ErrEmptyInnerKey
	ErrNonPositiveTS = omap.ErrNonPositiveTS
)

// API is a replicated two-level LWW-Map; all methods are concurrency-safe.
type API struct {
	mu sync.RWMutex
	m  *omap.OMap
}

// New returns an empty replica.
func New() *API { return &API{m: omap.New()} }

func put(o, i string, v int, ts int64, rep string) Op {
	return Op{Kind: Put, Outer: o, Inner: i, Value: v, TS: ts, Rep: rep}
}
func del(o string, ts int64) Op { return Op{Kind: DelOuter, Outer: o, TS: ts} }

// Apply validates then applies one op. Rejected ops change nothing.
func (a *API) Apply(op Op) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Apply(op)
}

// Merge incrementally merges other into a. Other is snapshotted under its
// read lock and merged under a's write lock, so no lock is held while
// acquiring the other (no lock-ordering deadlock).
func (a *API) Merge(other *API) {
	if a == other {
		return
	}
	other.mu.RLock()
	snap := omap.New()
	snap.CloneState(other.m)
	other.mu.RUnlock()
	a.mu.Lock()
	a.m.Merge(snap)
	a.mu.Unlock()
}

// View returns the filtered view as a fresh deep copy.
func (a *API) View() map[string]map[string]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.m.View()
}

// Tomb reports the tombstone timestamp of outer key o and whether it exists.
func (a *API) Tomb(o string) (int64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.m.Tomb(o)
}

// SelfCheck replays the built-in eight-step sequence and verifies the four
// invariants: batch-recompute result, tombstone propagation, keyed-LWW
// convergence (ts winner, both merge directions, rep tie-break) and failure
// without trace for the three distinct rejection causes. It uses only fresh
// local replicas, so it is safe to call concurrently.
func (a *API) SelfCheck() error {
	A, B := New(), New()
	must := func(r *API, ops ...Op) {
		for _, op := range ops {
			if err := r.Apply(op); err != nil {
				panic(err)
			}
		}
	}
	a1 := []Op{put("o", "k1", 100, 1, "A"), put("o", "k2", 200, 2, "A"), del("o", 3)}
	b1 := []Op{put("o", "k3", 300, 1, "B"), put("o", "k4", 400, 2, "B")}
	tail := []Op{put("o", "k5", 500, 3, "C"), put("o", "k6", 600, 4, "D")}
	must(A, a1...)
	must(B, b1...)
	A.Merge(B)
	must(A, tail...)
	want := map[string]map[string]int{"o": {"k6": 600}}
	if got := A.View(); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("invariant 1/2: eight-step view is %v, want %v", got, want)
	}
	if t, ok := A.Tomb("o"); !ok || t != 3 {
		return fmt.Errorf("invariant 2: tombstone not propagated: %d ok=%v", t, ok)
	}
	X, Y := New(), New()
	must(X, put("o", "k", 100, 5, "A"))
	must(Y, put("o", "k", 999, 2, "B"))
	X.Merge(Y)
	Y.Merge(X)
	if X.View()["o"]["k"] != 100 || Y.View()["o"]["k"] != 100 {
		return fmt.Errorf("invariant 3: ts winner wrong: X=%v Y=%v", X.View(), Y.View())
	}
	P, Q := New(), New()
	must(P, put("o", "t", 1, 5, "A"))
	must(Q, put("o", "t", 2, 5, "B"))
	P.Merge(Q)
	if P.View()["o"]["t"] != 2 {
		return fmt.Errorf("invariant 3: tie-break winner wrong: %v", P.View())
	}
	Z := New()
	must(Z, put("o", "keep", 7, 1, "A"))
	before := Z.View()
	bad := []Op{{Kind: Put, Outer: "", Inner: "x", Value: 1, TS: 1, Rep: "A"},
		put("o", "", 1, 1, "A"), put("o", "x", 1, 0, "A")}
	wantErr := []error{ErrEmptyOuterKey, ErrEmptyInnerKey, ErrNonPositiveTS}
	for i, op := range bad {
		if err := Z.Apply(op); err != wantErr[i] {
			return fmt.Errorf("invariant 4: op %d err=%v want %v", i, err, wantErr[i])
		}
	}
	if !reflect.DeepEqual(Z.View(), before) {
		return fmt.Errorf("invariant 4: rejected ops left a trace: %v", Z.View())
	}
	if err := Z.Apply(put("o", "after", 9, 2, "A")); err != nil {
		return fmt.Errorf("invariant 4: unusable after rejection: %v", err)
	}
	return nil
}
