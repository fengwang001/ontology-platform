// Package search builds an LSH index and answers approximate top-K queries.
package search

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// ErrNonFinite marks vectors containing NaN or ±Inf rejected at Add time.
var ErrNonFinite = errors.New("search: non-finite vector rejected")

// Index is a multi-table random-hyperplane LSH index over fixed-dim vectors.
type Index struct {
	dims, bits int
	fams       []*hyper.Family
	mu         sync.RWMutex
	tabs       *bucket.Multi
	vecs       []vec.Vec
	skipped    atomic.Int64
	dists      atomic.Uint64
}

// New creates an empty index; table t uses hyperplanes seeded with seed+t,
// so configs with fewer tables share a prefix of larger ones.
func New(dims, bits, tables int, seed int64) *Index {
	fams := make([]*hyper.Family, tables)
	for t := range fams {
		fams[t] = hyper.New(seed+int64(t), dims, bits)
	}
	return &Index{dims: dims, bits: bits, fams: fams, tabs: bucket.NewMulti(tables)}
}

// Add inserts v. NaN/±Inf vectors are rejected and counted in Skipped.
func (ix *Index) Add(v vec.Vec) error {
	if len(v) != ix.dims {
		return vec.DimError{Want: ix.dims, Got: len(v)}
	}
	if !vec.Finite(v) {
		ix.skipped.Add(1)
		return ErrNonFinite
	}
	sigs := make([]uint64, len(ix.fams))
	for t, f := range ix.fams {
		sigs[t] = f.Signature(v)
	}
	ix.mu.Lock()
	id := len(ix.vecs)
	ix.vecs = append(ix.vecs, v)
	ix.tabs.Add(sigs, id)
	ix.mu.Unlock()
	return nil
}

// Query returns the IDs of the approximate top-k nearest vectors to q.
func (ix *Index) Query(q vec.Vec, k int) ([]int, error) {
	if len(q) != ix.dims {
		return nil, vec.DimError{Want: ix.dims, Got: len(q)}
	}
	sigs := make([]uint64, len(ix.fams))
	for t, f := range ix.fams {
		sigs[t] = f.Signature(q)
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	cands := ix.tabs.Collect(sigs)
	ix.dists.Add(uint64(len(cands)))
	return topK(ix.vecs, q, cands, k), nil
}

// Len returns the number of indexed vectors.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.vecs)
}

// Skipped returns how many non-finite vectors Add has rejected.
func (ix *Index) Skipped() int64 { return ix.skipped.Load() }

// DistCount returns how many exact distances the rerank stage has computed.
func (ix *Index) DistCount() uint64 { return ix.dists.Load() }

// ResetStats zeroes the distance counter.
func (ix *Index) ResetStats() { ix.dists.Store(0) }

// HashCount sums hyperplane dot products computed across all tables.
func (ix *Index) HashCount() uint64 {
	var n uint64
	for _, f := range ix.fams {
		n += f.Computed()
	}
	return n
}

// ResetHashCount zeroes all per-table hash counters.
func (ix *Index) ResetHashCount() {
	for _, f := range ix.fams {
		f.ResetComputed()
	}
}

// Snapshot exposes families, tables and count for persistence; call only
// when no Add is in flight.
func (ix *Index) Snapshot() (fams []*hyper.Family, tabs *bucket.Multi, count int) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.fams, ix.tabs, len(ix.vecs)
}

type scored struct {
	id   int
	dist float64
}

func topK(vecs []vec.Vec, q vec.Vec, cands []int, k int) []int {
	s := make([]scored, 0, len(cands))
	for _, id := range cands {
		d, err := vec.Dist(vecs[id], q)
		if err == nil {
			s = append(s, scored{id, d})
		}
	}
	sort.Slice(s, func(i, j int) bool {
		if s[i].dist != s[j].dist {
			return s[i].dist < s[j].dist
		}
		return s[i].id < s[j].id
	})
	if k > len(s) {
		k = len(s)
	}
	out := make([]int, k)
	for i := range out {
		out[i] = s[i].id
	}
	return out
}

// BruteForce returns the exact top-k nearest IDs; the recall baseline.
func BruteForce(vecs []vec.Vec, q vec.Vec, k int) []int {
	ids := make([]int, len(vecs))
	for i := range ids {
		ids[i] = i
	}
	return topK(vecs, q, ids, k)
}
