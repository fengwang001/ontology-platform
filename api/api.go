// Package api is the external facade over the vector registry.
package api

import (
	"errors"

	"ontology/reg"
	"ontology/vv"
)

// Re-exported sentinel errors so callers can decide failure modes.
var (
	ErrNegativeCounter = reg.ErrNegativeCounter
	ErrNegativeActor   = reg.ErrNegativeActor
	ErrUnknownName     = reg.ErrUnknownName
)

// API is the entry point for all version-vector operations.
type API struct {
	r *reg.Registry
}

// New returns a ready-to-use API with an empty registry.
func New() *API {
	return &API{r: reg.New()}
}

// Set registers or replaces the named replica's vector.
func (a *API) Set(name string, v vv.Vector) error {
	return a.r.Set(name, v)
}

// Merge returns the join of two registered replicas' vectors.
func (a *API) Merge(nameA, nameB string) (vv.Vector, error) {
	return a.r.Merge(nameA, nameB)
}

// Compare returns the causal relation between two registered replicas.
func (a *API) Compare(nameA, nameB string) (vv.Relation, error) {
	return a.r.Compare(nameA, nameB)
}

func eq(a, b vv.Vector) bool {
	return vv.Compare(a, b) == vv.Equal
}

// SelfCheck replays the built-in seven-step sequence from NOTES.md on a
// fresh registry and verifies all four invariants. It returns nil iff every
// check holds.
func (a *API) SelfCheck() error {
	r := reg.New()
	must := func(err error) {
		if err != nil {
			panic("selfcheck: " + err.Error())
		}
	}
	must(r.Set("r1", vv.Vector{0: 1}))
	must(r.Set("r2", vv.Vector{0: 1, 1: 2}))
	must(r.Set("r3", vv.Vector{1: 1, 2: 1}))

	m4, err := r.Merge("r1", "r2") // S4
	if err != nil || !eq(m4, vv.Vector{0: 1, 1: 2}) {
		return errors.New("selfcheck: S4 merge mismatch")
	}
	m5, err := r.Merge("r2", "r3") // S5
	if err != nil || !eq(m5, vv.Vector{0: 1, 1: 2, 2: 1}) {
		return errors.New("selfcheck: S5 merge mismatch")
	}
	if c, err := r.Compare("r1", "r2"); err != nil || c != vv.Less { // S6
		return errors.New("selfcheck: S6 expected Less")
	}
	if c, err := r.Compare("r1", "r3"); err != nil || c != vv.Concurrent { // S7
		return errors.New("selfcheck: S7 expected Concurrent")
	}

	// Invariants 1-3: join laws and agreement with naive definitions.
	x, y := vv.Vector{0: 1, 2: 3}, vv.Vector{1: 2, 2: 1}
	j1, j2 := vv.Merge(x, y), vv.Merge(y, x)
	if !eq(j1, j2) {
		return errors.New("selfcheck: commutativity")
	}
	if !eq(vv.Merge(x, x), x) {
		return errors.New("selfcheck: idempotency")
	}
	if vv.Compare(j1, x) == vv.Less || vv.Compare(j1, y) == vv.Less {
		return errors.New("selfcheck: join not an upper bound")
	}

	// Invariant 4: rejected operations leave no trace; errors distinct.
	if err := r.Set("bad", vv.Vector{0: -1}); !errors.Is(err, ErrNegativeCounter) {
		return errors.New("selfcheck: negative counter error")
	}
	if err := r.Set("bad", vv.Vector{-1: 1}); !errors.Is(err, ErrNegativeActor) {
		return errors.New("selfcheck: negative actor error")
	}
	if _, err := r.Merge("r1", "bad"); !errors.Is(err, ErrUnknownName) {
		return errors.New("selfcheck: unknown name error")
	}
	if ErrNegativeCounter == ErrNegativeActor || ErrNegativeCounter == ErrUnknownName ||
		ErrNegativeActor == ErrUnknownName {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	if c, err := r.Compare("r1", "r2"); err != nil || c != vv.Less {
		return errors.New("selfcheck: registry state changed after rejection")
	}
	return nil
}
