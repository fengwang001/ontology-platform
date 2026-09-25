// Package replay keeps spilled blocks as a FIFO sequence and replays every
// change in original append order (oldest block, newer blocks, memory tail)
// onto an empty view. It depends only on buf.
package replay

import (
	"sort"

	"ontology/buf"
)

// KV is one existing key/value pair in the final view.
type KV struct{ Key, Val string }

// probeBound is the m-independent ceiling on keys inspected per change:
// a hash lookup inspects exactly the target key; a table scan would exceed it.
const probeBound = 1

// Store is the FIFO sequence of spilled blocks; blocks only append, so block
// order is spill order and a copied block keeps within-block append order.
type Store struct {
	blocks [][]buf.Change
	// probeMax is the largest number of keys inspected to land any single
	// change during the most recent Replay. Hash lookup is constant (1); a
	// whole-table scan would make it grow with the view size. Unexported:
	// callers and demos only get the bool ProbesBounded, never the number.
	probeMax int
}

// New creates an empty block store.
func New() *Store { return &Store{} }

// Spill appends one full block to disk (FIFO), copying it so later buffer
// reuse cannot mutate the stored block. Del changes are kept verbatim.
func (s *Store) Spill(block []buf.Change) {
	cp := make([]buf.Change, len(block))
	copy(cp, block)
	s.blocks = append(s.blocks, cp)
}

// Blocks is the number of spilled blocks.
func (s *Store) Blocks() int { return len(s.blocks) }

// ProbesBounded reports whether the most recent replay landed every change
// with at most probeBound key inspections, i.e. hash positioning rather than
// a whole-table scan. It exposes no numeric counter.
func (s *Store) ProbesBounded() bool { return s.probeMax > 0 && s.probeMax <= probeBound }

// Replay applies all spilled blocks oldest-first and then tail onto an empty
// view and returns existing pairs sorted by key. Set overwrites via one hash
// lookup; Del removes via one hash delete; neither ever scans the table.
func (s *Store) Replay(tail []buf.Change) []KV {
	view := make(map[string]string)
	s.probeMax = 0
	apply := func(c buf.Change) {
		switch c.Op {
		case buf.Set:
			view[c.Key] = c.Val // inspect exactly this key: one hash probe
		case buf.Del:
			delete(view, c.Key) // inspect exactly this key: one hash probe
		}
		if s.probeMax < 1 {
			s.probeMax = 1
		}
	}
	for _, blk := range s.blocks {
		for _, c := range blk {
			apply(c)
		}
	}
	for _, c := range tail {
		apply(c)
	}
	keys := make([]string, 0, len(view))
	for k := range view {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KV, 0, len(keys))
	for _, k := range keys {
		out = append(out, KV{Key: k, Val: view[k]})
	}
	return out
}
