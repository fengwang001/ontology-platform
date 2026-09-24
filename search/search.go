// Package search implements multi-table hyperplane-LSH ANN with exact reranking.
package search

import (
	"errors"
	"sort"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

// Result is one ranked hit.
type Result struct {
	ID   bucket.ID
	Dist float64
}

// Stats are readable counters.
type Stats struct {
	Vectors   int
	Skipped   int // rejected vectors (NaN/Inf)
	HashCalls int64
}

// snapshot is an immutable, fully-built index generation.
type snapshot struct {
	dim, bits, tables int
	seed              int64
	fam               *hyper.Family
	mt                *bucket.MultiTable
	vectors           []vec.Vec
	skipped           int
}

// Index holds an atomically published immutable snapshot (read-only queries).
type Index struct {
	cur atomic.Pointer[snapshot]
	// distanceCount counts rerank distance evaluations across queries.
	distanceCount atomic.Int64
}

// Build constructs an index from vectors; NaN/Inf vectors are rejected/counted.
func Build(dim, bits, tables int, seed int64, vectors []vec.Vec) (*Index, Stats, error) {
	if dim <= 0 || bits <= 0 || bits > 64 || tables <= 0 {
		return nil, Stats{}, errors.New("search: invalid parameters")
	}
	snap, err := buildSnap(dim, bits, tables, seed, vectors)
	if err != nil {
		return nil, Stats{}, err
	}
	idx := &Index{}
	idx.cur.Store(snap)
	return idx, Stats{
		Vectors: len(snap.vectors), Skipped: snap.skipped, HashCalls: snap.fam.HashCount,
	}, nil
}

func buildSnap(dim, bits, tables int, seed int64, vectors []vec.Vec) (*snapshot, error) {
	fam := hyper.NewFamily(dim, bits, tables, seed)
	mt := bucket.NewMultiTable(tables)
	kept := make([]vec.Vec, 0, len(vectors))
	skipped := 0
	for _, x := range vectors {
		if len(x) != dim {
			return nil, vec.DimError{Want: dim, Got: len(x)}
		}
		if err := vec.Validate(x); err != nil {
			skipped++
			continue
		}
		id := bucket.ID(len(kept))
		sigs := make([]bucket.Signature, tables)
		for t := 0; t < tables; t++ {
			s, err := fam.Signature(t, x)
			if err != nil {
				return nil, err
			}
			sigs[t] = bucket.Signature(s)
		}
		mt.Add(sigs, id)
		kept = append(kept, append(vec.Vec(nil), x...))
	}
	return &snapshot{
		dim: dim, bits: bits, tables: tables, seed: seed,
		fam: fam, mt: mt, vectors: kept, skipped: skipped,
	}, nil
}

// Replace atomically swaps in a freshly built generation; in-flight queries
// keep using the old full generation, new queries see the complete new one.
func (idx *Index) Replace(dim, bits, tables int, seed int64, vectors []vec.Vec) (Stats, error) {
	snap, err := buildSnap(dim, bits, tables, seed, vectors)
	if err != nil {
		return Stats{}, err
	}
	idx.cur.Store(snap)
	return Stats{
		Vectors: len(snap.vectors), Skipped: snap.skipped, HashCalls: snap.fam.HashCount,
	}, nil
}

// Snapshot inspectors used by persist.
func (idx *Index) snapshot() *snapshot { return idx.cur.Load() }

// Query generates LSH candidates and exact-reranks the top-k by Euclidean dist.
func (idx *Index) Query(q vec.Vec, k int) ([]Result, int, error) {
	snap := idx.cur.Load()
	if snap == nil {
		return nil, 0, nil
	}
	if len(q) != snap.dim {
		return nil, 0, vec.DimError{Want: snap.dim, Got: len(q)}
	}
	sigs := make([]bucket.Signature, snap.tables)
	for t := 0; t < snap.tables; t++ {
		s, err := snap.fam.Signature(t, q)
		if err != nil {
			return nil, 0, err
		}
		sigs[t] = bucket.Signature(s)
	}
	cand := snap.mt.Candidates(sigs)
	rs := make([]Result, 0, len(cand))
	for _, id := range cand {
		d, err := vec.L2(q, snap.vectors[id])
		if err != nil {
			return nil, 0, err
		}
		idx.distanceCount.Add(1)
		rs = append(rs, Result{ID: id, Dist: d})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Dist != rs[j].Dist {
			return rs[i].Dist < rs[j].Dist
		}
		return rs[i].ID < rs[j].ID
	})
	if k < len(rs) {
		rs = rs[:k]
	}
	return rs, len(cand), nil
}

// BruteForce returns the exact top-k (recall baseline); distances counted too.
func (idx *Index) BruteForce(q vec.Vec, k int) ([]Result, error) {
	snap := idx.cur.Load()
	if snap == nil {
		return nil, nil
	}
	if len(q) != snap.dim {
		return nil, vec.DimError{Want: snap.dim, Got: len(q)}
	}
	rs := make([]Result, 0, len(snap.vectors))
	for id, x := range snap.vectors {
		d, err := vec.L2(q, x)
		if err != nil {
			return nil, err
		}
		idx.distanceCount.Add(1)
		rs = append(rs, Result{ID: bucket.ID(id), Dist: d})
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Dist != rs[j].Dist {
			return rs[i].Dist < rs[j].Dist
		}
		return rs[i].ID < rs[j].ID
	})
	if k < len(rs) {
		rs = rs[:k]
	}
	return rs, nil
}

// ResetDistances zeroes the rerank distance counter and returns its prior value.
func (idx *Index) ResetDistances() int64 { return idx.distanceCount.Swap(0) }

// Len returns the number of indexed (kept) vectors.
func (idx *Index) Len() int {
	if s := idx.cur.Load(); s != nil {
		return len(s.vectors)
	}
	return 0
}
