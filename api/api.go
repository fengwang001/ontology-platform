// Package api is the public face of the semi-join engine.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"

	"ontology/semi"
)

// Re-exported sentinel errors; all five are mutually distinguishable.
var (
	ErrBadMaxLeft   = semi.ErrBadMaxLeft
	ErrLeftExists   = semi.ErrLeftExists
	ErrLeftNotFound = semi.ErrLeftNotFound
	ErrTooManyLeft  = semi.ErrTooManyLeft
	ErrRefNegative  = semi.ErrRefNegative
)

// Engine is a semi-join view over incremental left/right tables.
type Engine struct{ e *semi.Engine }

// New creates an Engine holding at most maxLeft left rows.
func New(maxLeft int) (*Engine, error) {
	e, err := semi.New(maxLeft)
	if err != nil {
		return nil, err
	}
	return &Engine{e: e}, nil
}

// AddLeft inserts a left row {id, key}; id must be unique.
func (g *Engine) AddLeft(id int64, key *string) error { return g.e.AddLeft(id, key) }

// DelLeft removes a left row; unknown ids are rejected.
func (g *Engine) DelLeft(id int64) error { return g.e.DelLeft(id) }

// AddRight increments the key's right-table refcount.
func (g *Engine) AddRight(key *string) error { return g.e.AddRight(key) }

// DelRight decrements the key's refcount; going negative is rejected.
func (g *Engine) DelRight(key *string) error { return g.e.DelRight(key) }

// View returns the retained left ids in ascending order.
func (g *Engine) View() []int64 { return g.e.View() }

func sp(s string) *string { return &s }

// SelfCheck verifies the four invariants against built-in op sequences.
// It runs on fresh internal engines, so it is safe to call concurrently.
func (g *Engine) SelfCheck() error {
	// Invariants 1+2: random ops; incremental view == batch view, ids unique.
	r := rand.New(rand.NewSource(1))
	e, err := semi.New(4096)
	if err != nil {
		return err
	}
	keys := []string{"a", "b", "c", "d"}
	for i := 0; i < 400; i++ {
		k := keys[r.Intn(len(keys))]
		switch r.Intn(4) {
		case 0:
			_ = e.AddLeft(int64(i), &k)
		case 1:
			_ = e.DelLeft(int64(r.Intn(i + 1)))
		case 2:
			_ = e.AddRight(&k)
		case 3:
			_ = e.DelRight(&k)
		}
		v := e.View()
		if !slices.Equal(v, e.BatchView()) {
			return fmt.Errorf("selfcheck: view != batch at step %d", i)
		}
		if !slices.IsSorted(v) || len(slices.Compact(slices.Clone(v))) != len(v) {
			return fmt.Errorf("selfcheck: duplicate id in view at step %d", i)
		}
	}
	// Invariant 3: ref never goes negative; NULL never matches.
	e2, _ := semi.New(1)
	if err := e2.DelRight(sp("x")); !errors.Is(err, semi.ErrRefNegative) {
		return fmt.Errorf("selfcheck: negative ref accepted")
	}
	_ = e2.AddRight(nil)
	_ = e2.AddLeft(9, nil)
	if len(e2.View()) != 0 {
		return fmt.Errorf("selfcheck: NULL key matched")
	}
	// Invariant 4: rejected ops leave no trace; the engine keeps working.
	before := e2.View()
	for _, c := range []struct {
		err  error
		want error
	}{
		{e2.AddLeft(9, nil), semi.ErrLeftExists},
		{e2.AddLeft(8, sp("y")), semi.ErrTooManyLeft},
		{e2.DelLeft(99), semi.ErrLeftNotFound},
		{e2.DelRight(sp("x")), semi.ErrRefNegative},
	} {
		if !errors.Is(c.err, c.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", c.want, c.err)
		}
	}
	if _, err := semi.New(0); !errors.Is(err, semi.ErrBadMaxLeft) {
		return fmt.Errorf("selfcheck: bad maxLeft accepted")
	}
	if !slices.Equal(e2.View(), before) {
		return fmt.Errorf("selfcheck: rejected op mutated state")
	}
	if e2.DelRight(nil) != nil || e2.DelLeft(9) != nil {
		return fmt.Errorf("selfcheck: state corrupted by rejected ops")
	}
	return nil
}
