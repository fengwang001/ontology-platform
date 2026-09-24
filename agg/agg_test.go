package agg_test

import (
	"math"
	"testing"

	"ontology/agg"
)

func TestAggregators(t *testing.T) {
	cases := []struct {
		name  string
		k     agg.Kind
		vals  []float64
		want  float64
		del   float64
		need  bool
		after float64
	}{
		{"count", agg.Count, []float64{1, 2, 3}, 3, 2, false, 2},
		{"sum", agg.Sum, []float64{1, 2, 3}, 6, 3, false, 3},
		{"min-non-extreme", agg.Min, []float64{1, 2, 3}, 1, 2, false, 1},
		{"min-extreme", agg.Min, []float64{1, 2, 3}, 1, 1, true, 2},
		{"max-extreme", agg.Max, []float64{1, 2, 3}, 3, 3, true, 2},
		{"distinct", agg.DistinctCount, []float64{1, 1, 2}, 2, 1, true, 2},
		{"signed-zero", agg.Min, []float64{0, -0, 5}, 0, math.Copysign(0, -1), true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := agg.New(tc.k)
			for _, v := range tc.vals {
				a.Insert(v)
			}
			if a.Value() != tc.want {
				t.Fatalf("value=%v want %v", a.Value(), tc.want)
			}
			need := a.Delete(tc.del)
			if need != tc.need {
				t.Fatalf("delete needMembers=%v want %v", need, tc.need)
			}
			if got := agg.NeedsMembersOnDelete(tc.k, tc.del, tc.want); got != tc.need {
				t.Fatalf("NeedsMembersOnDelete=%v want %v", got, tc.need)
			}
			if tc.need {
				a.Reset()
				seen := map[float64]bool{}
				for _, v := range tc.vals {
					if v == tc.del && !seen[v] {
						seen[v] = true
						continue
					}
					a.Insert(v)
				}
			}
			if a.Value() != tc.after {
				t.Fatalf("after=%v want %v", a.Value(), tc.after)
			}
		})
	}
}
