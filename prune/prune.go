// Package prune defines the projection, computes the pruned (kept)
// source-column set as the union of all output refs, and produces the
// projected materialized view row by row. It depends only on col.
package prune

import (
	"errors"
	"sync"

	"ontology/col"
)

var (
	ErrUnknownRef   = errors.New("prune: refs unknown source column")
	ErrDuplicateOut = errors.New("prune: duplicate output name")
)

// Output is one projected column: Out is the downstream-visible name,
// Refs the source columns it sums over.
type Output struct {
	Out  string
	Refs []string
}

// Engine holds the projection and the materialized view. The zero
// value is not usable; build one with NewEngine.
type Engine struct {
	mu     sync.RWMutex
	schema *col.Schema
	outs   []Output
	kept   []string // union of all refs, in schema order
	view   [][]int
	reads  int // source-column reads for projection in the last Apply
}

// NewEngine builds an engine over a validated source schema.
func NewEngine(schema *col.Schema) *Engine {
	return &Engine{schema: schema}
}

// SetProjection validates outs and atomically replaces the projection.
// Unknown refs or duplicate output names fail without touching state.
func (e *Engine) SetProjection(outs []Output) error {
	seen := make(map[string]bool, len(outs))
	need := make(map[string]bool)
	for _, o := range outs {
		if seen[o.Out] {
			return ErrDuplicateOut
		}
		seen[o.Out] = true
		for _, r := range o.Refs {
			if !e.schema.Has(r) {
				return ErrUnknownRef
			}
			need[r] = true
		}
	}
	kept := make([]string, 0, len(need))
	for _, name := range e.schema.Names() {
		if need[name] {
			kept = append(kept, name)
		}
	}
	cp := make([]Output, len(outs))
	copy(cp, outs)
	e.mu.Lock()
	e.outs = cp
	e.kept = kept
	e.mu.Unlock()
	return nil
}

// Apply validates the change row and appends its projected output.
// A rejected row changes nothing (projection, view, counters).
func (e *Engine) Apply(m map[string]int) error {
	row, err := e.schema.NewRow(m) // missing / unknown source column
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]int, len(e.outs))
	reads := 0
	for i, o := range e.outs {
		out[i] = row.Eval(o.Refs) // reads exactly the kept refs, never pruned cols
		reads += len(o.Refs)
	}
	e.view = append(e.view, out)
	e.reads = reads
	return nil
}

// KeptCols returns the retained source columns (union of all refs),
// in schema order. Pruned columns never appear here.
func (e *Engine) KeptCols() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.kept))
	copy(out, e.kept)
	return out
}

// OutputNames returns the output column names in projection order.
func (e *Engine) OutputNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.outs))
	for i, o := range e.outs {
		out[i] = o.Out
	}
	return out
}

// View returns a deep copy of the projected materialized view:
// one row per applied change row, values in projection order.
func (e *Engine) View() [][]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([][]int, len(e.view))
	for i, r := range e.view {
		cp := make([]int, len(r))
		copy(cp, r)
		out[i] = cp
	}
	return out
}
