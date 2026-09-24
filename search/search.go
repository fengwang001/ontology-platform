// Package search builds multi-table LSH indexes and answers approximate
// nearest-neighbour queries with exact re-ranking over hash candidates.
package search

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/persist"
	"ontology/vec"
)

// ErrInvalidVector marks vectors containing NaN or +/-Inf.
var ErrInvalidVector = errors.New("search: vector contains NaN or Inf")

// Index is a multi-table LSH index over fixed-dimension vectors.
type Index struct {
	dim, bits, ntab int
	seed            int64

	mu      sync.Mutex
	vecs    []vec.Vec
	skipped int

	snap    atomic.Pointer[snapshot]
	distOps atomic.Int64
	hashOps atomic.Int64
}

// snapshot is an immutable, atomically published view of the index.
type snapshot struct {
	vecs   []vec.Vec
	tables []*bucket.Table
}

// New creates an empty index for dim-dimensional vectors.
func New(dim, bits, ntab int, seed int64) *Index {
	return &Index{dim: dim, bits: bits, ntab: ntab, seed: seed}
}

// Add appends v to the pending set; NaN/Inf vectors are rejected and counted.
func (ix *Index) Add(v vec.Vec) error {
	if err := vec.Check(v, ix.dim); err != nil {
		return err
	}
	if !vec.Valid(v) {
		ix.mu.Lock()
		ix.skipped++
		ix.mu.Unlock()
		return ErrInvalidVector
	}
	ix.mu.Lock()
	ix.vecs = append(ix.vecs, v)
	ix.mu.Unlock()
	return nil
}

// Build hashes all pending vectors into ntab fresh tables and publishes
// them atomically: concurrent queries see either the old or the new
// complete snapshot, never a half-built table.
func (ix *Index) Build() {
	ix.mu.Lock()
	vecs := make([]vec.Vec, len(ix.vecs))
	copy(vecs, ix.vecs)
	ix.mu.Unlock()

	tables := make([]*bucket.Table, ix.ntab)
	for t := range tables {
		tbl := bucket.NewTable(hyper.New(ix.seed+int64(t), ix.dim, ix.bits))
		for id, v := range vecs {
			tbl.Add(id, v)
		}
		tables[t] = tbl
	}
	var ops int64
	for _, tbl := range tables {
		ops += tbl.Family().HashOps()
	}
	ix.hashOps.Store(ops)
	ix.snap.Store(&snapshot{vecs: vecs, tables: tables})
}

// candidates returns the deduplicated candidate IDs for q without
// computing any distances.
func (ix *Index) candidates(q vec.Vec) ([]int, *snapshot) {
	snap := ix.snap.Load()
	if snap == nil {
		return nil, nil
	}
	seen := make(map[int]struct{})
	for _, tbl := range snap.tables {
		for _, id := range tbl.Candidates(q) {
			if id < len(snap.vecs) {
				seen[id] = struct{}{}
			}
		}
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, snap
}

// query finds the top-k candidates by exact distance; it also reports
// how many distances were computed in the re-ranking phase.
func (ix *Index) query(q vec.Vec, k int) (top []int, ranked int, err error) {
	if err := vec.Check(q, ix.dim); err != nil {
		return nil, 0, err
	}
	ids, snap := ix.candidates(q)
	if snap == nil {
		return nil, 0, nil
	}
	type cand struct {
		id int
		d  float64
	}
	cs := make([]cand, len(ids))
	for i, id := range ids {
		cs[i] = cand{id, vec.Dist(snap.vecs[id], q)}
	}
	ix.distOps.Add(int64(len(cs)))
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].id < cs[j].id
	})
	if k > len(cs) {
		k = len(cs)
	}
	top = make([]int, k)
	for i := range top {
		top[i] = cs[i].id
	}
	return top, len(cs), nil
}

// Query returns the IDs of the approximate k nearest neighbours of q.
func (ix *Index) Query(q vec.Vec, k int) ([]int, error) {
	top, _, err := ix.query(q, k)
	return top, err
}

// CandidateCount reports how many distinct candidates q would re-rank.
func (ix *Index) CandidateCount(q vec.Vec) (int, error) {
	if err := vec.Check(q, ix.dim); err != nil {
		return 0, err
	}
	ids, _ := ix.candidates(q)
	return len(ids), nil
}

// DistOps reports the cumulative number of exact distances computed.
func (ix *Index) DistOps() int64 { return ix.distOps.Load() }

// HashOps reports the hashing dot products of the last Build.
func (ix *Index) HashOps() int64 { return ix.hashOps.Load() }

// Skipped reports how many vectors were rejected as NaN/Inf.
func (ix *Index) Skipped() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.skipped
}

// Snapshot exports the built LSH structure for persistence.
func (ix *Index) Snapshot() *persist.Data {
	snap := ix.snap.Load()
	if snap == nil {
		return nil
	}
	d := &persist.Data{Dim: ix.dim, Bits: ix.bits, NTables: ix.ntab,
		NVecs: len(snap.vecs), Seed: ix.seed}
	for _, tbl := range snap.tables {
		d.Planes = append(d.Planes, tbl.Family().Planes())
		d.Tables = append(d.Tables, tbl.Buckets())
	}
	return d
}

// Load rebuilds an index structure (without vectors) from persisted data.
func Load(d *persist.Data) *Index {
	ix := New(d.Dim, d.Bits, d.NTables, d.Seed)
	tables := make([]*bucket.Table, d.NTables)
	for i := range tables {
		tables[i] = bucket.FromBuckets(hyper.FromPlanes(d.Planes[i]), d.Tables[i])
	}
	ix.snap.Store(&snapshot{tables: tables})
	return ix
}

// BruteForce returns the exact top-k IDs by distance, the recall baseline.
func BruteForce(vecs []vec.Vec, q vec.Vec, k int) []int {
	type cand struct {
		id int
		d  float64
	}
	cs := make([]cand, len(vecs))
	for i, v := range vecs {
		cs[i] = cand{i, vec.Dist(v, q)}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].id < cs[j].id
	})
	if k > len(cs) {
		k = len(cs)
	}
	out := make([]int, k)
	for i := range out {
		out[i] = cs[i].id
	}
	return out
}
