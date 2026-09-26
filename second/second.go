// Package second builds the second level of a two-level perfect hash:
// per-bucket collision-free (a,b) search, direct slots for singletons.
// It depends only on first.
package second

import (
	"sync/atomic"

	"ontology/first"
)

type bucket struct {
	direct bool   // singleton bucket: key stored inline, no table
	key    int    // direct-slot key
	a, b   int    // h(x) = (a*x + b mod p) mod len(slots)
	slots  []int  // second-level table, len == n_j^2
	filled []bool // occupancy bitmap for slots
}

// Table is a built two-level perfect-hash structure, immutable after Build.
type Table struct {
	m, p    int
	n       int
	buckets []bucket
	probes  int64 // slots checked by the most recent Lookup; unexported on purpose
}

// mod is mathematical mod, result always in [0, m).
func mod(x, m int) int {
	r := x % m
	if r < 0 {
		r += m
	}
	return r
}

// Build constructs the structure. Inputs must be pre-validated by the caller:
// distinct keys, m >= 1, p prime and greater than every key.
func Build(keys []int, m, p int) *Table {
	t := &Table{m: m, p: p, n: len(keys), buckets: make([]bucket, m)}
	for j, ks := range first.Partition(keys, m) {
		switch len(ks) {
		case 0: // empty bucket: no table at all
		case 1:
			t.buckets[j] = bucket{direct: true, key: ks[0]}
		default:
			t.buckets[j] = search(ks, p)
		}
	}
	return t
}

// search tries a=1,2,... and, innermost, b=0,1,... until the bucket's keys
// hash to pairwise distinct slots of a size n^2 table.
func search(keys []int, p int) bucket {
	n := len(keys)
	size := n * n
	for a := 1; ; a++ {
		for b := 0; b < p; b++ {
			bk := bucket{a: a, b: b, slots: make([]int, size), filled: make([]bool, size)}
			ok := true
			for _, k := range keys {
				s := bk.slot(k, p)
				if bk.filled[s] {
					ok = false
					break
				}
				bk.filled[s] = true
				bk.slots[s] = k
			}
			if ok {
				return bk
			}
		}
	}
}

func (bk *bucket) slot(x, p int) int {
	return mod(mod(bk.a*x+bk.b, p), len(bk.slots))
}

// Lookup reports whether x was built into the table, recording how many
// slots it checked (first level + second level; a direct slot counts 1).
func (t *Table) Lookup(x int) bool {
	probes := 1 // first-level bucket
	bk := &t.buckets[first.Bucket(x, t.m)]
	found := false
	switch {
	case bk.direct:
		found = bk.key == x
	case len(bk.slots) > 0:
		probes++
		s := bk.slot(x, t.p)
		found = bk.filled[s] && bk.slots[s] == x
	}
	atomic.StoreInt64(&t.probes, int64(probes))
	return found
}

// Keys returns every key stored in the structure.
func (t *Table) Keys() []int {
	out := make([]int, 0, t.n)
	for i := range t.buckets {
		bk := &t.buckets[i]
		if bk.direct {
			out = append(out, bk.key)
			continue
		}
		for s, f := range bk.filled {
			if f {
				out = append(out, bk.slots[s])
			}
		}
	}
	return out
}

// Space returns the total allocated second-level table size and, recomputed
// independently from the stored keys, the expected sum of n_j^2 (direct
// slots count 0).
func (t *Table) Space() (got, want int) {
	for i := range t.buckets {
		got += len(t.buckets[i].slots)
	}
	for _, ks := range first.Partition(t.Keys(), t.m) {
		if len(ks) > 1 {
			want += len(ks) * len(ks)
		}
	}
	return got, want
}

// Describe reports bucket j's shape: direct slot or (a, b, table size).
func (t *Table) Describe(j int) (direct bool, a, b, size int) {
	bk := &t.buckets[j]
	return bk.direct, bk.a, bk.b, len(bk.slots)
}

// Slot returns the second-level slot of x in hashed bucket j.
func (t *Table) Slot(j, x int) int {
	return t.buckets[j].slot(x, t.p)
}
