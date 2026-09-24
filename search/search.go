// Package search builds random-hyperplane LSH indexes and serves approximate
// top-k nearest-neighbour queries with an exact brute-force baseline.
package search

import (
	"errors"
	"sort"
	"sync"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// ErrNotReady is returned while the index build has not been published.
var ErrNotReady = errors.New("search: index not ready")

// Hit is one ranked result.
type Hit struct {
	ID       int
	Distance float64
}

// Index is a read-mostly LSH index.
type Index struct {
	family *hyper.Family

	mu      sync.RWMutex
	vectors []vec.Vec
	set     *bucket.Set
	ready   bool

	skipped   int
	hashCalls int

	distMu      sync.Mutex
	distCounter int
}

// NewIndex allocates an empty index for the given geometry.
func NewIndex(dim, tables, bits int, seed int64) (*Index, error) {
	f, err := hyper.New(dim, tables, bits, seed)
	if err != nil {
		return nil, err
	}
	return &Index{family: f, set: bucket.NewSet(tables)}, nil
}

// Dim reports the vector dimension.
func (ix *Index) Dim() int { return ix.family.Dim }

// Family exposes the hyperplane family (used by persistence).
func (ix *Index) Family() *hyper.Family { return ix.family }

// Add inserts one vector. NaN/±Inf vectors are rejected and counted as skipped.
func (ix *Index) Add(x vec.Vec) error {
	if err := vec.Validate(x); err != nil {
		ix.mu.Lock()
		ix.skipped++
		ix.mu.Unlock()
		return err
	}
	if len(x) != ix.family.Dim {
		return vec.DimMismatchError(ix.family.Dim, len(x))
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	id := len(ix.vectors)
	ix.vectors = append(ix.vectors, append(vec.Vec{}, x...))

	sigs := make([]uint64, ix.family.Tables)
	for t := 0; t < ix.family.Tables; t++ {
		sig, err := ix.family.Signature(t, x)
		if err != nil {
			return err
		}
		sigs[t] = sig
		ix.hashCalls += ix.family.Bits
	}
	for t, sig := range sigs {
		ix.set.Add(t, sig, id)
	}
	return nil
}

// Publish atomically makes the built tables visible to queries.
func (ix *Index) Publish() {
	ix.mu.Lock()
	ix.ready = true
	ix.mu.Unlock()
}

// Skipped reports how many invalid vectors were rejected.
func (ix *Index) Skipped() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.skipped
}

// HashCalls reports total inner-product hash evaluations (N*L*b).
func (ix *Index) HashCalls() int { return ix.hashCalls }

// resetDistances zeroes the ranking distance counter.
func (ix *Index) resetDistances() {
	ix.distMu.Lock()
	ix.distCounter = 0
	ix.distMu.Unlock()
}

// Distances returns the ranking distance computations since last reset.
func (ix *Index) Distances() int {
	ix.distMu.Lock()
	defer ix.distMu.Unlock()
	return ix.distCounter
}

type snapshot struct {
	vectors []vec.Vec
	set     *bucket.Set
}

func (ix *Index) load() (snapshot, func(), error) {
	ix.mu.RLock()
	if !ix.ready {
		ix.mu.RUnlock()
		return snapshot{}, nil, ErrNotReady
	}
	return snapshot{vectors: ix.vectors, set: ix.set}, ix.mu.RUnlock, nil
}

// Query returns the approximate top-k nearest neighbours of q.
func (ix *Index) Query(q vec.Vec, k int) ([]Hit, int, error) {
	snap, release, err := ix.load()
	if err != nil {
		return nil, 0, err
	}
	defer release()
	if err := vec.Validate(q); err != nil {
		return nil, 0, err
	}
	if len(q) != ix.family.Dim {
		return nil, 0, vec.DimMismatchError(ix.family.Dim, len(q))
	}
	sigs := make([]uint64, ix.family.Tables)
	for t := range sigs {
		sigs[t], _ = ix.family.Signature(t, q)
	}
	cand := snap.set.Candidates(sigs)
	hits := ix.rank(snap.vectors, q, cand, k)
	return hits, len(cand), nil
}

func (ix *Index) rank(vectors []vec.Vec, q vec.Vec, cand []int, k int) []Hit {
	hits := make([]Hit, 0, len(cand))
	for _, id := range cand {
		d, err := vec.Euclidean(vectors[id], q)
		if err != nil {
			continue
		}
		ix.distMu.Lock()
		ix.distCounter++
		ix.distMu.Unlock()
		hits = append(hits, Hit{ID: id, Distance: d})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Distance != hits[j].Distance {
			return hits[i].Distance < hits[j].Distance
		}
		return hits[i].ID < hits[j].ID
	})
	if k < len(hits) {
		hits = hits[:k]
	}
	return hits
}

// BruteForce returns the exact top-k by scanning every vector.
func (ix *Index) BruteForce(q vec.Vec, k int) ([]Hit, error) {
	snap, release, err := ix.load()
	if err != nil {
		return nil, err
	}
	defer release()
	all := make([]int, len(snap.vectors))
	for i := range all {
		all[i] = i
	}
	return ix.rank(snap.vectors, q, all, k), nil
}

// Recall is |approx ∩ exact| / k for equal-length top-k result sets.
func Recall(approx, exact []Hit) float64 {
	if len(exact) == 0 {
		return 1
	}
	have := map[int]bool{}
	for _, h := range approx {
		have[h.ID] = true
	}
	n := 0
	for _, h := range exact {
		if have[h.ID] {
			n++
		}
	}
	return float64(n) / float64(len(exact))
}
