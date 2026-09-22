// Package query provides point (stabbing), overlap and batch queries
// over a tree.Tree. Query results are sorted by ival.Compare, making
// them deterministic and independent of insertion order.
package query

import (
	"errors"
	"slices"
	"sync/atomic"

	"ontology/ival"
	"ontology/node"
	"ontology/tree"
)

// ErrBatchTooLarge is returned when a batch exceeds the configured limit.
var ErrBatchTooLarge = errors.New("query: batch size limit exceeded")

// Querier runs read-only queries against a tree. Query methods are
// safe for concurrent use once the tree is no longer being mutated.
type Querier struct {
	tree     *tree.Tree
	maxBatch int
	visited  atomic.Int64 // nodes visited by the most recent query
}

// Option configures a Querier.
type Option func(*Querier)

// WithMaxBatch caps the number of intervals per Batch call; 0 is unlimited.
func WithMaxBatch(n int) Option { return func(q *Querier) { q.maxBatch = n } }

// New returns a Querier over t.
func New(t *tree.Tree, opts ...Option) *Querier {
	q := &Querier{tree: t}
	for _, o := range opts {
		o(q)
	}
	return q
}

// Stab returns every interval containing point p, sorted.
func (q *Querier) Stab(p int64) []ival.Interval {
	q.visited.Store(0)
	var out []ival.Interval
	q.stab(q.tree.Root(), p, &out)
	return sorted(out)
}

func (q *Querier) stab(n *node.Node, p int64, out *[]ival.Interval) {
	if n == nil {
		return
	}
	q.visited.Add(1)
	if node.MaxHiOf(n.Left) > p {
		q.stab(n.Left, p, out)
	}
	if n.Iv.Contains(p) {
		*out = append(*out, n.Iv)
	}
	if n.Iv.Lo <= p {
		q.stab(n.Right, p, out)
	}
}

// Overlap returns every interval overlapping iv, sorted. An invalid
// interval (Lo > Hi) is a decidable error; an empty interval matches
// nothing, consistent with ival.Overlaps.
func (q *Querier) Overlap(iv ival.Interval) ([]ival.Interval, error) {
	if !iv.Valid() {
		return nil, ival.ErrInvalid
	}
	q.visited.Store(0)
	var out []ival.Interval
	q.overlap(q.tree.Root(), iv, &out)
	return sorted(out), nil
}

func (q *Querier) overlap(n *node.Node, iv ival.Interval, out *[]ival.Interval) {
	if n == nil {
		return
	}
	q.visited.Add(1)
	if node.MaxHiOf(n.Left) > iv.Lo {
		q.overlap(n.Left, iv, out)
	}
	if ival.Overlaps(n.Iv, iv) {
		*out = append(*out, n.Iv)
	}
	if n.Iv.Lo < iv.Hi {
		q.overlap(n.Right, iv, out)
	}
}

// Batch runs Overlap for each interval and returns one result slice
// per input, in order. An oversized batch is rejected before any work.
func (q *Querier) Batch(ivs []ival.Interval) ([][]ival.Interval, error) {
	if q.maxBatch > 0 && len(ivs) > q.maxBatch {
		return nil, ErrBatchTooLarge
	}
	out := make([][]ival.Interval, len(ivs))
	for i, iv := range ivs {
		r, err := q.Overlap(iv)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

func sorted(ivs []ival.Interval) []ival.Interval {
	slices.SortFunc(ivs, ival.Compare)
	return ivs
}
