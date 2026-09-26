package vv

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

type pair struct{ a, b Vector }

func unionKeys(a, b Vector) map[int]struct{} {
	s := map[int]struct{}{}
	for k := range a {
		s[k] = struct{}{}
	}
	for k := range b {
		s[k] = struct{}{}
	}
	return s
}

// naiveMerge is the spec: enumerate the union of keys, max per key.
func naiveMerge(a, b Vector) Vector {
	out := Vector{}
	for k := range unionKeys(a, b) { // every union key is assigned, even value 0
		out[k] = max(a[k], b[k])
	}
	return out
}

// naiveCompare is the spec: per-key <=/>= with missing keys treated as 0.
func naiveCompare(a, b Vector) Relation {
	aLE, bLE := true, true
	for k := range unionKeys(a, b) {
		av, bv := a[k], b[k]
		if av > bv {
			aLE = false
		}
		if av < bv {
			bLE = false
		}
	}
	switch {
	case aLE && bLE:
		return Equal
	case aLE:
		return Less
	case bLE:
		return Greater
	default:
		return Concurrent
	}
}

// rndPairs generates random cases (varied overlap) in a loop, fixed seed.
func rndPairs(n int) []pair {
	rng := rand.New(rand.NewPCG(1, 2))
	ps := make([]pair, n)
	for i := range ps {
		a, b := Vector{}, Vector{}
		for _, k := range rng.Perm(12)[:2+rng.IntN(5)] {
			a[k] = rng.IntN(4)
		}
		for _, k := range rng.Perm(12)[:2+rng.IntN(5)] {
			b[k] = rng.IntN(4)
		}
		ps[i] = pair{a, b}
	}
	return ps
}

// TestMergeMatchesNaive pins invariant 1: Merge equals naive recomputation.
func TestMergeMatchesNaive(t *testing.T) {
	for i, p := range rndPairs(200) {
		if got, want := Merge(p.a, p.b), naiveMerge(p.a, p.b); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d: got %v, want %v", i, got, want)
		}
	}
}

// TestMergeJoinLaws pins invariant 2: commutativity, idempotence, upper bound.
func TestMergeJoinLaws(t *testing.T) {
	for _, p := range rndPairs(200) {
		j := Merge(p.a, p.b)
		if !reflect.DeepEqual(j, Merge(p.b, p.a)) || !reflect.DeepEqual(Merge(p.a, p.a), p.a) {
			t.Fatal("commutativity or idempotence broken")
		}
		if Compare(j, p.a) == Less || Compare(j, p.b) == Less {
			t.Fatalf("join %v is below an input", j)
		}
	}
}

// TestCompareTrichotomy pins invariant 3 against the naive definition.
func TestCompareTrichotomy(t *testing.T) {
	for i, p := range rndPairs(200) {
		if got, want := Compare(p.a, p.b), naiveCompare(p.a, p.b); got != want {
			t.Fatalf("case %d: got %s, want %s", i, got, want)
		}
	}
}

// TestCompareTable covers boundary semantics explicitly.
func TestCompareTable(t *testing.T) {
	cases := []struct {
		name   string
		a, b   Vector
		expect Relation
	}{
		{"both empty", Vector{}, Vector{}, Equal},
		{"missing key is zero", Vector{0: 1}, Vector{0: 1, 1: 0}, Equal},
		{"strict less", Vector{0: 1}, Vector{0: 1, 1: 2}, Less},
		{"strict greater", Vector{0: 2}, Vector{0: 1}, Greater},
		{"disjoint concurrent", Vector{0: 1}, Vector{1: 1}, Concurrent},
		{"mixed concurrent", Vector{0: 1, 2: 1}, Vector{1: 1, 2: 2}, Concurrent},
		{"zero extra key equal", Vector{1: 0}, Vector{}, Equal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compare(c.a, c.b); got != c.expect {
				t.Fatalf("got %s, want %s", got, c.expect)
			}
		})
	}
}

// TestMergeReadCountSparse proves sparse representation: reads stay a small
// constant multiple of the 4 nonzero entries and do not grow with m. The
// counter is unexported; only this in-package test can read it.
func TestMergeReadCountSparse(t *testing.T) {
	var baseline int64 = -1
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		got := Merge(Vector{m - 1: 1, m - 2: 2}, Vector{m - 3: 3, m - 4: 4})
		reads := mergeStats.reads.Load()
		if reads > 16 { // <= small constant multiple of 4 nonzero entries
			t.Fatalf("m=%d: read %d entries, bound is 16", m, reads)
		}
		if baseline >= 0 && reads != baseline {
			t.Fatalf("read count grows with m: %d then %d", baseline, reads)
		}
		baseline = reads
		if len(got) != 4 {
			t.Fatalf("m=%d: merged size %d, want 4", m, len(got))
		}
	}
}
