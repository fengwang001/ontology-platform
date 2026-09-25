// Package search builds an LSH index and answers approximate TopK queries.
package search

import (
	"sort"
	"sync"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// Index combines a hyperplane family, bucket tables and the raw vectors.
type Index struct {
	fam  *hyper.Family
	bk   *bucket.Index
	dim  int
	mu   sync.RWMutex // guards vecs
	vecs []vec.Vector

	distOps atomic.Int64 // distances computed in the rerank phase
	skipped atomic.Int64 // vectors rejected by Add (NaN/Inf/dim)
}

// New creates an empty index; seed makes the hyperplanes reproducible.
func New(dim, bits, tables int, seed int64) *Index {
	return &Index{
		fam: hyper.New(seed, dim, bits, tables),
		bk:  bucket.New(tables),
		dim: dim,
	}
}

// Add validates and indexes v. Rejected vectors are counted in Skipped.
func (ix *Index) Add(v vec.Vector) error {
	if err := vec.Check(v, ix.dim); err != nil {
		ix.skipped.Add(1)
		return err
	}
	sigs, err := ix.fam.Sign(v)
	if err != nil {
		ix.skipped.Add(1)
		return err
	}
	ix.mu.Lock()
	id := len(ix.vecs)
	ix.vecs = append(ix.vecs, v)
	ix.mu.Unlock()
	ix.bk.Add(id, sigs)
	return nil
}

// Query returns the IDs of the approximate top-k nearest vectors.
func (ix *Index) Query(q vec.Vector, k int) ([]int, error) {
	if err := vec.Check(q, ix.dim); err != nil {
		return nil, err
	}
	sigs, err := ix.fam.Sign(q)
	if err != nil {
		return nil, err
	}
	cands := ix.bk.Candidates(sigs)
	ix.distOps.Add(int64(len(cands)))
	type scored struct {
		id int
		d  float64
	}
	sc := make([]scored, 0, len(cands))
	ix.mu.RLock()
	for _, id := range cands {
		sc = append(sc, scored{id, vec.Dist(q, ix.vecs[id])})
	}
	ix.mu.RUnlock()
	sort.Slice(sc, func(i, j int) bool {
		if sc[i].d != sc[j].d {
			return sc[i].d < sc[j].d
		}
		return sc[i].id < sc[j].id
	})
	if k > len(sc) {
		k = len(sc)
	}
	out := make([]int, k)
	for i := range out {
		out[i] = sc[i].id
	}
	return out, nil
}

// BruteForce is the exact baseline: top-k over all vectors by distance.
func BruteForce(vecs []vec.Vector, q vec.Vector, k int) []int {
	type scored struct {
		id int
		d  float64
	}
	sc := make([]scored, len(vecs))
	for i, v := range vecs {
		sc[i] = scored{i, vec.Dist(q, v)}
	}
	sort.Slice(sc, func(i, j int) bool {
		if sc[i].d != sc[j].d {
			return sc[i].d < sc[j].d
		}
		return sc[i].id < sc[j].id
	})
	if k > len(sc) {
		k = len(sc)
	}
	out := make([]int, k)
	for i := range out {
		out[i] = sc[i].id
	}
	return out
}

// DistOps reports distances computed in the rerank phase since ResetDistOps.
func (ix *Index) DistOps() int64 { return ix.distOps.Load() }

// ResetDistOps zeroes the rerank distance counter.
func (ix *Index) ResetDistOps() { ix.distOps.Store(0) }

// Skipped reports how many vectors Add rejected.
func (ix *Index) Skipped() int64 { return ix.skipped.Load() }

// Len returns the number of indexed vectors.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.vecs)
}

// Family exposes the hyperplane family (persistence, counters).
func (ix *Index) Family() *hyper.Family { return ix.fam }

// Buckets exposes the bucket tables (persistence).
func (ix *Index) Buckets() *bucket.Index { return ix.bk }
