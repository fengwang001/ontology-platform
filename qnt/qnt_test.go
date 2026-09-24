package qnt

import (
	"slices"
	"strconv"
	"testing"
)

func naive(model []int64) (sorted []int64, med float64, p90 int64) {
	sorted = slices.Clone(model)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		med = float64(sorted[n/2])
	} else {
		med = float64(sorted[n/2-1])/2 + float64(sorted[n/2])/2
	}
	p90 = sorted[(90*n+99)/100-1]
	return
}

// Invariant 1: after any op sequence, Median/QuantileP90 equal the naive
// batch recomputation over the sorted multiset (duplicates included).
func TestAgainstNaive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		steps, mod int
	}{
		{"small", 200, 7}, {"medium", 500, 97}, {"large", 1000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e Engine
			var model []int64
			for i := 0; i < tc.steps; i++ {
				if len(model) == 0 || (i*7+3)%5 != 4 {
					v := int64((i*31+17)%tc.mod) - int64(tc.mod/2)
					e.Insert(v)
					model = append(model, v)
				} else {
					j := (i * 11) % len(model)
					if err := e.Delete(model[j]); err != nil {
						t.Fatalf("step %d delete: %v", i, err)
					}
					model = slices.Delete(model, j, j+1)
				}
				if len(model) == 0 {
					if _, err := e.Median(); err != ErrEmpty {
						t.Fatalf("step %d: empty median err=%v", i, err)
					}
					continue
				}
				sorted, med, p90 := naive(model)
				m, err1 := e.Median()
				q, err2 := e.QuantileP90()
				if e.Count() != len(sorted) || err1 != nil || err2 != nil || m != med || q != p90 {
					t.Fatalf("step %d: got (%v,%v,%d) want (%v,%v,%d)", i, m, q, e.Count(), med, p90, len(sorted))
				}
			}
		})
	}
}

// Invariant 3: Kth(1..Count) is the full ascending sequence and Count
// equals inserts minus deletes.
func TestKthSequence(t *testing.T) {
	var e Engine
	var model []int64
	for i := 0; i < 300; i++ {
		v := int64((i*13 + 5) % 11) // heavy duplicates
		e.Insert(v)
		model = append(model, v)
	}
	for i := 0; i < 100; i++ {
		j := (i * 7) % len(model)
		if err := e.Delete(model[j]); err != nil {
			t.Fatal(err)
		}
		model = slices.Delete(model, j, j+1)
	}
	slices.Sort(model)
	if e.Count() != 200 || e.Count() != len(model) {
		t.Fatalf("count %d, want 200", e.Count())
	}
	for k := 1; k <= e.Count(); k++ {
		if v, err := e.Kth(k); err != nil || v != model[k-1] {
			t.Fatalf("kth(%d)=%v,%v want %v", k, v, err, model[k-1])
		}
	}
	if _, err := e.Kth(0); err == nil {
		t.Fatal("kth(0) should fail")
	}
	if _, err := e.Kth(e.Count() + 1); err == nil {
		t.Fatal("kth(n+1) should fail")
	}
	if err := e.Delete(999); err == nil || e.Count() != 200 {
		t.Fatal("delete of absent value must fail and keep state")
	}
}

// Invariant 2: Median <= QuantileP90 on any non-empty multiset.
func TestMedianLeP90(t *testing.T) {
	for _, tc := range []struct {
		name string
		vals []int64
	}{
		{"single", []int64{5}}, {"dups", []int64{3, 3, 3, 3}},
		{"neg", []int64{-9, -1, -4, -4, 0}}, {"spread", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e Engine
			for _, v := range tc.vals {
				e.Insert(v)
			}
			m, err1 := e.Median()
			q, err2 := e.QuantileP90()
			if err1 != nil || err2 != nil || m > float64(q) {
				t.Fatalf("median %v,%v > p90 %v,%v", m, err1, q, err2)
			}
		})
	}
}

// Complexity: nodes visited by one Median stay within 2*ceil(log2 m)+2
// (and < 64) as m grows — a height-bounded descent, not a full scan.
// Reads the unexported counter directly (same-package test only).
func TestVisitBound(t *testing.T) {
	for _, m := range []int{101, 1001, 9999} {
		t.Run(strconv.Itoa(m), func(t *testing.T) {
			var e Engine
			for i := 0; i < m; i++ {
				e.Insert(int64(i)*2 + 1)
			}
			if _, err := e.Median(); err != nil {
				t.Fatal(err)
			}
			ceilLog2 := 0
			for x := 1; x < m; x <<= 1 {
				ceilLog2++
			}
			if v := e.lastVisits.Load(); v > int64(2*ceilLog2+2) || v >= 64 {
				t.Fatalf("m=%d: visited %d nodes, bound %d", m, v, 2*ceilLog2+2)
			}
		})
	}
}
