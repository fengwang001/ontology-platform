package second

import (
	"math/rand"
	"testing"

	"ontology/first"
)

func randKeys(rng *rand.Rand, n, bound int) []int {
	set := make(map[int]struct{}, n)
	for len(set) < n {
		set[rng.Intn(bound)] = struct{}{}
	}
	out := make([]int, 0, n)
	for k := range set {
		out = append(out, k)
	}
	return out
}

// O(1) addressing: probes per Lookup stay a small constant independent of m.
// m values are prime: a composite m lets n_j^2 divide every in-bucket key
// difference, which is the known worst case for the mandated (a,b) order.
func TestConstantProbes(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, m := range []int{101, 1009, 5003, 9973} {
		keys := randKeys(rng, m, 10*m)
		tbl := Build(keys, m, 100003)
		for _, x := range []int{keys[0], keys[m-1], 10*m + 1} {
			tbl.Lookup(x)
			if p := tbl.probes; p < 1 || p > 2 {
				t.Fatalf("m=%d x=%d probes=%d, want 1..2", m, x, p)
			}
		}
	}
}

// Every bucket's keys occupy pairwise distinct slots; built keys are found.
func TestNoCollision(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, c := range []struct{ n, m int }{{6, 7}, {100, 17}, {1000, 101}} {
		keys := randKeys(rng, c.n, 10*c.n)
		tbl := Build(keys, c.m, 100003)
		for j := range tbl.buckets {
			bk := &tbl.buckets[j]
			seen := map[int]bool{}
			for _, k := range keys {
				if first.Bucket(k, c.m) != j || bk.direct {
					continue
				}
				s := bk.slot(k, tbl.p)
				if seen[s] {
					t.Fatalf("n=%d m=%d bucket %d slot %d collides", c.n, c.m, j, s)
				}
				seen[s] = true
			}
		}
		for _, k := range keys {
			if !tbl.Lookup(k) {
				t.Fatalf("n=%d m=%d: built key %d not found", c.n, c.m, k)
			}
		}
	}
}

// Total second-level space equals the independent sum of n_j^2.
func TestSpace(t *testing.T) {
	tbl := Build([]int{5, 11, 13, 17, 19, 24}, 6, 29)
	if got, want := tbl.Space(); got != want || want != 13 {
		t.Fatalf("6-key: got=%d want=%d, expect 13", got, want)
	}
	rng := rand.New(rand.NewSource(9))
	for _, c := range []struct{ n, m int }{{50, 53}, {1000, 127}} {
		tbl := Build(randKeys(rng, c.n, 10*c.n), c.m, 100003)
		if got, want := tbl.Space(); got != want {
			t.Fatalf("n=%d m=%d: got=%d want=%d", c.n, c.m, got, want)
		}
	}
}
