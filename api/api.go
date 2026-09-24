// Package api is the public entry point for incremental UNION DISTINCT
// maintenance; it depends only on uni. Rejected ops fail with distinct
// sentinel errors and leave all state untouched.
package api

import (
	"errors"
	"fmt"

	"ontology/uni"
)

var (
	// ErrInvalidNPart is returned by New when nPart <= 0.
	ErrInvalidNPart = errors.New("api: number of partitions must be positive")
	// ErrPartitionOutOfRange is returned for p < 0 or p >= nPart.
	ErrPartitionOutOfRange = errors.New("api: partition out of range")
	// ErrEmptyElement is returned when the element is the empty string.
	ErrEmptyElement = errors.New("api: element must be a non-empty string")
)

// Change re-exports uni.Change so callers use package api alone.
type Change = uni.Change

// Engine is the materialized union view with its changelog.
type Engine struct {
	nPart int
	u     *uni.Engine
}

// New creates an engine over nPart partitions.
func New(nPart int) (*Engine, error) {
	if nPart <= 0 {
		return nil, ErrInvalidNPart
	}
	return &Engine{nPart: nPart, u: uni.New(nPart)}, nil
}

// Op is one batch operation: Add==true delivers "+Elem".
type Op struct {
	Add  bool
	P    int
	Elem string
}

// Add delivers "+elem" to p; a duplicate inside p is a no-op.
func (e *Engine) Add(p int, elem string) error {
	if err := e.check(p, elem); err != nil {
		return err
	}
	e.u.Add(p, elem)
	return nil
}

// Remove withdraws "elem" from p; withdrawing an absent element is a no-op.
func (e *Engine) Remove(p int, elem string) error {
	if err := e.check(p, elem); err != nil {
		return err
	}
	e.u.Remove(p, elem)
	return nil
}

// Apply validates the whole batch first: one invalid op rejects it all
// (sets, cnt, changelog, View unchanged), wrapping the sentinel reason.
func (e *Engine) Apply(ops []Op) error {
	for i, o := range ops {
		if err := e.check(o.P, o.Elem); err != nil {
			return fmt.Errorf("api: op %d: %w", i, err)
		}
	}
	for _, o := range ops {
		if o.Add {
			e.u.Add(o.P, o.Elem)
		} else {
			e.u.Remove(o.P, o.Elem)
		}
	}
	return nil
}

// check is the single failure gate: it performs no mutation.
func (e *Engine) check(p int, elem string) error {
	if p < 0 || p >= e.nPart {
		return ErrPartitionOutOfRange
	}
	if elem == "" {
		return ErrEmptyElement
	}
	return nil
}

// View returns the sorted distinct union of all partitions.
func (e *Engine) View() []string { return e.u.View() }

// Changes returns a copy of the ordered changelog.
func (e *Engine) Changes() []Change { return e.u.Changes() }

// SelfCheck runs the built-in sequence (NOTES.md 8 steps, duplicate and
// missing idempotency, three rejection kinds, an all-or-nothing batch) on
// a fresh engine, verifying against an independent model after each
// step: sets match, cnt==holders>=1, and changelog prefixes replay with
// strictly alternating +e/-e. The receiver is untouched.
func (e *Engine) SelfCheck() error {
	c, err := New(e.nPart)
	if err != nil {
		return err
	}
	seq := []Op{
		{true, 0, "a"}, {true, 1, "a"}, {true, 0, "b"},
		{false, 0, "a"}, {false, 1, "a"}, {false, 0, "b"},
		{true, 1, "b"}, {false, 0, "b"},
		{true, 0, "a"}, {true, 0, "a"}, // duplicate Add: no-op
		{false, 1, "z"}, // missing Remove: no-op
	}
	model := make([]map[string]struct{}, e.nPart)
	for i := range model {
		model[i] = map[string]struct{}{}
	}
	for i, o := range seq {
		if err := c.Apply([]Op{o}); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if o.Add {
			model[o.P][o.Elem] = struct{}{}
		} else {
			delete(model[o.P], o.Elem)
		}
		if err := c.u.SelfCheck(model); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	// Batch with one invalid middle op is rejected atomically.
	snap := fmt.Sprint(c.View(), c.Changes())
	batch := []Op{{true, 0, "q"}, {true, e.nPart, "y"}, {false, 0, "q"}}
	if c.Apply(batch) == nil || fmt.Sprint(c.View(), c.Changes()) != snap {
		return errors.New("invalid batch not rejected atomically")
	}
	// Three distinct rejection kinds, each individually trace-free.
	for i, o := range []Op{{true, -1, "q"}, {false, e.nPart, "q"}, {true, 0, ""}} {
		if c.Apply([]Op{o}) == nil || fmt.Sprint(c.View(), c.Changes()) != snap {
			return fmt.Errorf("rejection %d not trace-free", i+1)
		}
	}
	model[0]["q"] = struct{}{} // engine stays usable afterwards
	if err := c.Add(0, "q"); err != nil {
		return fmt.Errorf("engine unusable after rejection: %w", err)
	}
	return c.u.SelfCheck(model)
}
