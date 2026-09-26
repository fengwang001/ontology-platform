package api

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

// Probe: max rank error ratio err/(eps*n) over many configs after the fix.
func TestProbeMaxError(t *testing.T) {
	worst := 0.0
	for _, eps := range []float64{0.25, 0.1, 0.05, 0.01} {
		for _, m := range []int{8, 50, 100, 1000, 5000} {
			for _, order := range []string{"asc", "desc", "rand", "rand2"} {
				vals := make([]int64, m)
				for i := range vals {
					vals[i] = int64(i) + 1
				}
				switch order {
				case "desc":
					slices.Reverse(vals)
				case "rand":
					rand.New(rand.NewSource(7)).Shuffle(m, func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })
				case "rand2":
					rand.New(rand.NewSource(99)).Shuffle(m, func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })
				}
				a, err := New(eps)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range vals {
					if err := a.Insert(v); err != nil {
						t.Fatal(err)
					}
				}
				slices.Sort(vals)
				maxErr := 0.0
				for k := 1; k < 100; k++ {
					phi := float64(k) / 100
					got, err := a.Query(phi)
					if err != nil {
						t.Fatal(err)
					}
					rank, _ := slices.BinarySearch(vals, got)
					if d := math.Abs(float64(rank+1) - phi*float64(m)); d > maxErr {
						maxErr = d
					}
				}
				ratio := maxErr / (eps * float64(m))
				if ratio > worst {
					worst = ratio
				}
				if ratio > 1 {
					t.Logf("eps=%v m=%d %s: maxErr=%v eps*n=%v ratio=%.2f",
						eps, m, order, maxErr, eps*float64(m), ratio)
				}
			}
		}
	}
	t.Logf("worst ratio err/(eps*n) = %.3f", worst)
}
