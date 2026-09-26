package second

import (
	"math/rand"
	"testing"

	"ontology/first"
)

func nextPrime(after int) int {
	p := after + 1
	for {
		prime := p >= 2
		for d := 2; d*d <= p; d++ {
			if p%d == 0 {
				prime = false
				break
			}
		}
		if prime {
			return p
		}
		p++
	}
}

// pairedKeys returns m distinct keys arranged in colliding pairs:
// i and i+m both hash to bucket i, so every used bucket has n_j=2.
func pairedKeys(m int) []int {
	half := m / 2
	keys := make([]int, 0, 2*half)
	for i := 0; i < half; i++ {
		keys = append(keys, i, m+i)
	}
	return keys
}

// TestProbeCountConstant is the white-box proof of O(1) addressing: across
// several scales of m, one Lookup checks a constant number of slots (<=2),
// read straight from the unexported counter (never via the public API).
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 333, 1000, 3333, 10000} {
		keys := pairedKeys(m)
		p := nextPrime(2 * m) // p > every key
		tab := Build(first.Partition(keys, m), p)
		for _, k := range keys {
			if got, ok := tab.Lookup(k); !ok || got != k {
				t.Fatalf("m=%d Lookup(%d)=(%d,%v), want (%d,true)", m, k, got, ok, k)
			}
			if tab.probes.Load() > 2 {
				t.Fatalf("m=%d Lookup checked %d slots, want constant <=2", m, tab.probes.Load())
			}
		}
		// An absent key mapping to an empty bucket checks only level 1.
		if _, ok := tab.Lookup(5*m + 1); ok {
			t.Fatalf("m=%d absent key reported found", m)
		}
		if tab.probes.Load() < 1 || tab.probes.Load() > 2 {
			t.Fatalf("m=%d absent-key probes=%d, want 1..2", m, tab.probes.Load())
		}
	}
}

// TestSixKeySecondLevel pins the two non-singleton rows of the NOTES table:
// bucket 1 {13,19} -> (a,b)=(1,0) over 4 slots at 1,3; bucket 5
// {5,11,17} -> (1,0) over 9 slots at 5,2,8.
func TestSixKeySecondLevel(t *testing.T) {
	keys := []int{5, 11, 13, 17, 19, 24}
	tab := Build(first.Partition(keys, 6), 29)
	b := tab.buckets
	if !b[0].Direct || b[0].Single != 24 {
		t.Fatalf("bucket 0 = %+v, want direct slot 24", b[0])
	}
	if b[1].Direct || b[1].A != 1 || b[1].B != 0 || b[1].Mj != 4 ||
		!b[1].Used[1] || b[1].Slots[1] != 13 || !b[1].Used[3] || b[1].Slots[3] != 19 {
		t.Fatalf("bucket 1 = %+v, want (1,0) slots 13->1,19->3", b[1])
	}
	if b[5].Direct || b[5].A != 1 || b[5].B != 0 || b[5].Mj != 9 ||
		!b[5].Used[5] || b[5].Slots[5] != 5 ||
		!b[5].Used[2] || b[5].Slots[2] != 11 ||
		!b[5].Used[8] || b[5].Slots[8] != 17 {
		t.Fatalf("bucket 5 = %+v, want (1,0) slots 5->5,11->2,17->8", b[5])
	}
	if tab.SecondSize() != 13 {
		t.Fatalf("SecondSize=%d, want 9+4=13", tab.SecondSize())
	}
}

// TestSixKeyPartition pins the first six rows of the NOTES table: h1 buckets
// for {5,11,13,17,19,24} at m=6.
func TestSixKeyPartition(t *testing.T) {
	want := map[int]int{5: 5, 11: 5, 13: 1, 17: 5, 19: 1, 24: 0}
	for k, b := range want {
		if got := first.H1(k, 6); got != b {
			t.Errorf("H1(%d)=%d, want %d", k, got, b)
		}
	}
	part := first.Partition([]int{5, 11, 13, 17, 19, 24}, 6)
	got := map[int][]int{}
	for j, bk := range part {
		got[j] = bk
	}
	for _, c := range []struct {
		j    int
		keys []int
	}{{0, []int{24}}, {1, []int{13, 19}}, {5, []int{5, 11, 17}}} {
		if len(got[c.j]) != len(c.keys) {
			t.Fatalf("bucket %d = %v, want %v", c.j, got[c.j], c.keys)
		}
		for i, k := range c.keys {
			if got[c.j][i] != k {
				t.Fatalf("bucket %d = %v, want %v", c.j, got[c.j], c.keys)
			}
		}
	}
}

// TestSpaceSum loops random key sets: the built second-level size must
// equal the independently accumulated Σ n_j² (singleton/empty buckets = 0).
func TestSpaceSum(t *testing.T) {
	for iter := 0; iter < 40; iter++ {
		m := 2 + rand.Intn(50)
		n := 1 + rand.Intn(4*m)
		pool := rand.Perm(8 * m)
		keys := make([]int, 0, n)
		for i := 0; i < n; i++ {
			keys = append(keys, pool[i])
		}
		tab := Build(first.Partition(keys, m), nextPrime(8*m))
		want := 0
		for _, bk := range first.Partition(keys, m) {
			if len(bk) > 1 {
				want += len(bk) * len(bk)
			}
		}
		if tab.SecondSize() != want {
			t.Fatalf("iter %d: SecondSize=%d, want Σn²=%d", iter, tab.SecondSize(), want)
		}
	}
}
