package gc

import (
	"errors"
	"math/rand"
	"testing"
)

// naiveMerge is the reference: union of entries, per-entry max.
func naiveMerge(a, b map[int]int) map[int]int {
	out := map[int]int{}
	for n, c := range a {
		out[n] = c
	}
	for n, c := range b {
		if c > out[n] {
			out[n] = c
		}
	}
	return out
}

func equalMap(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for n, c := range a {
		if b[n] != c {
			return false
		}
	}
	return true
}

func TestMergeMatchesNaive(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ma, mb := map[int]int{}, map[int]int{}
		for i := 0; i < rng.Intn(8); i++ {
			ma[rng.Intn(1000)] = rng.Intn(100)
		}
		for i := 0; i < rng.Intn(8); i++ {
			mb[rng.Intn(1000)] = rng.Intn(100)
		}
		ca, _ := FromMap(ma)
		cb, _ := FromMap(mb)
		merged, err := Merge(ca, cb)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !equalMap(merged.Snapshot(), naiveMerge(ma, mb)) {
			t.Fatalf("seed %d: merge != naive max", seed)
		}
	}
}

func TestValueMatchesNaive(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m, sum := map[int]int{}, 0
		for i := 0; i < rng.Intn(20); i++ {
			n, c := rng.Intn(500), rng.Intn(50)
			sum += c - m[n]
			m[n] = c
		}
		c, _ := FromMap(m)
		if Value(c) != sum {
			t.Fatalf("seed %d: Value=%d want %d", seed, Value(c), sum)
		}
	}
}

func TestMergeCommutativeIdempotent(t *testing.T) {
	cases := [][2]map[int]int{
		{{0: 5}, {1: 3}},
		{{0: 5, 2: 1}, {1: 3, 2: 9}},
		{{}, {}},
		{{7: 1}, {7: 1}},
	}
	for i, tc := range cases {
		a, _ := FromMap(tc[0])
		b, _ := FromMap(tc[1])
		ab, _ := Merge(a, b)
		ba, _ := Merge(b, a)
		aa, _ := Merge(a, a)
		if !equalMap(ab.Snapshot(), ba.Snapshot()) {
			t.Fatalf("case %d: not commutative", i)
		}
		if !equalMap(aa.Snapshot(), a.Snapshot()) {
			t.Fatalf("case %d: not idempotent", i)
		}
	}
}

// TestMergeReadsSparse proves the sparse-map representation: with
// node id space m and only 2 non-zero entries per side, Merge reads
// a constant number of entries independent of m.
func TestMergeReadsSparse(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := FromMap(map[int]int{0: 1, m - 1: 2})
		b, _ := FromMap(map[int]int{1: 1, m - 2: 3})
		if _, err := Merge(a, b); err != nil {
			t.Fatal(err)
		}
		if got := lastMergeReads.Load(); got > 4 { // 2*(nnz per side)
			t.Fatalf("m=%d: read %d entries, grows with m", m, got)
		}
	}
}

func TestFaultInjection(t *testing.T) {
	c := New()
	if err := c.Inc(0, 5); err != nil {
		t.Fatal(err)
	}
	bad, _ := FromMap(map[int]int{0: 1})
	bad.entries[0] = -1 // same-package fault injection
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"non-positive inc", c.Inc(0, 0), ErrNonPositiveInc},
		{"negative inc", c.Inc(0, -3), ErrNonPositiveInc},
		{"negative node", c.Inc(-1, 1), ErrNegativeNode},
		{"frommap neg entry", func() error { _, e := FromMap(map[int]int{0: -1}); return e }(), ErrNegativeEntry},
		{"frommap neg node", func() error { _, e := FromMap(map[int]int{-1: 1}); return e }(), ErrNegativeNode},
		{"merge neg entry", func() error { _, e := Merge(c, bad); return e }(), ErrNegativeEntry},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, tc.err, tc.want)
		}
	}
	if Value(c) != 5 { // no rejected call left a trace
		t.Fatalf("state changed by rejected ops: %d", Value(c))
	}
	if ErrNonPositiveInc == ErrNegativeNode || ErrNegativeNode == ErrNegativeEntry ||
		ErrNonPositiveInc == ErrNegativeEntry {
		t.Fatal("sentinel errors must be distinct")
	}
}
