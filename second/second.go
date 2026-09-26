// Package second builds the collision-free per-bucket second-level tables.
package second

import (
	"sync/atomic"

	"ontology/first"
)

// Bucket is one first-level bucket: either a direct slot (n_j == 1) or an
// n_j^2 slot second-level table indexed by h_j(x)=(a*x+b mod p) mod m_j.
type Bucket struct {
	Direct bool
	Single int // the sole key when Direct
	A, B   int // second-level hash parameters
	Mj     int // m_j = n_j^2 when not Direct
	Slots  []int
	Used   []bool
}

// Table is the assembled second level over all first-level buckets.
type Table struct {
	buckets []*Bucket
	m       int
	p       int
	probes  atomic.Int64 // unexported: slots checked by the most recent Lookup (level 1 + level 2)
}

// hash computes h_j(x) = (a*x + b mod p) mod m_j.
func hash(x, a, b, p, mj int) int {
	v := (a*x + b) % p
	if v < 0 {
		v += p
	}
	r := v % mj
	if r < 0 {
		r += mj
	}
	return r
}

// buildBucket finds the first (a, b), scanning a=1,2,.. then b=0,1,.., under
// which every key lands in a distinct slot, and returns the fixed Bucket.
func buildBucket(keys []int, p int) *Bucket {
	if len(keys) == 1 {
		return &Bucket{Direct: true, Single: keys[0]}
	}
	n := len(keys)
	mj := n * n
	// Parameters are residues mod p: scan a=1..p-1, inner b=0..p-1.
	for a := 1; a < p; a++ {
		for b := 0; b < p; b++ {
			slots := make([]int, mj)
			used := make([]bool, mj)
			ok := true
			for _, k := range keys {
				s := hash(k, a, b, p, mj)
				if used[s] {
					ok = false
					break
				}
				used[s] = true
				slots[s] = k
			}
			if ok {
				return &Bucket{A: a, B: b, Mj: mj, Slots: slots, Used: used}
			}
		}
	}
	// Unreachable for prime p > every key: averaged over uniform (a,b) the
	// expected colliding pairs is n(n-1)/(2 n^2) < 1/2, so a zero-collision
	// choice must exist.
	panic("fks: no collision-free second hash (p not prime or p <= a key)")
}

// Build constructs a collision-free table from first-level partitions.
func Build(partitions [][]int, p int) *Table {
	t := &Table{buckets: make([]*Bucket, len(partitions)), m: len(partitions), p: p}
	for j, keys := range partitions {
		if len(keys) > 0 {
			t.buckets[j] = buildBucket(keys, p)
		}
	}
	return t
}

// Lookup locates x. It always compares the stored key with x: an empty slot
// or a different stored key means the key is absent. It records the number of
// slots checked (first-level bucket counts as 1; direct slot counts as 1).
func (t *Table) Lookup(x int) (int, bool) {
	t.probes.Store(1) // first level: selecting the bucket
	b := t.buckets[first.H1(x, t.m)]
	if b == nil {
		return 0, false
	}
	if b.Direct {
		t.probes.Store(2) // level-1 bucket + direct slot
		if b.Single == x {
			return b.Single, true
		}
		return 0, false
	}
	s := hash(x, b.A, b.B, t.p, b.Mj)
	t.probes.Store(2) // level-1 bucket + one second-level slot
	if !b.Used[s] {
		return 0, false
	}
	k := b.Slots[s]
	if k == x {
		return k, true
	}
	return 0, false
}

// SecondSize reports the sum of all second-level table sizes (Σ n_j²);
// direct slots contribute 0.
func (t *Table) SecondSize() int {
	total := 0
	for _, b := range t.buckets {
		if b != nil && !b.Direct {
			total += b.Mj
		}
	}
	return total
}

// Buckets exposes bucket count for the api layer's self check.
func (t *Table) Buckets() []*Bucket { return t.buckets }
