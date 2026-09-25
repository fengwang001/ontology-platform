package agg

import (
	"math"
	"testing"

	"ontology/change"
)

type aggCase struct {
	name    string
	kind    Kind
	seq     []float64
	del     float64
	delOK   bool
	rec     []float64
	want    float64
	present bool
}

func TestAggregatorBehavior(t *testing.T) {
	cases := []aggCase{
		{"count", Count, []float64{1, 2, 3}, 2, true, nil, 2, true},
		{"count-recompute", Count, nil, 0, true, []float64{4, 5}, 2, true},
		{"sum", Sum, []float64{1.5, 2.5}, 1.5, true, nil, 2.5, true},
		{"min-hit", Min, []float64{3, 1, 2}, 1, false, []float64{3, 2}, 2, true},
		{"min-tie", Min, []float64{1, 1, 5}, 1, false, []float64{1, 5}, 1, true},
		{"min-miss", Min, []float64{3, 1, 2}, 3, true, nil, 1, true},
		{"max-hit", Max, []float64{3, 9, 2}, 9, false, []float64{3, 2}, 3, true},
		{"distinct", DistinctCount, []float64{1, 1, 2}, 1, false, []float64{1, 1, 3}, 2, true},
		{"distinct-zero-sign", DistinctCount, []float64{0, math.Copysign(0, -1)}, 0, false,
			[]float64{0, math.Copysign(0, -1)}, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(tc.kind)
			for _, v := range tc.seq {
				a.Insert(v)
			}
			if got := a.Delete(tc.del); got != tc.delOK {
				t.Fatalf("Delete ok = %v, want %v", got, tc.delOK)
			}
			if tc.rec != nil {
				a.Recompute(tc.rec)
			}
			got, present := a.Value()
			if present != tc.present || math.Float64bits(got) != math.Float64bits(tc.want) {
				t.Fatalf("Value = %v(present=%v), want %v(present=%v)", got, present, tc.want, tc.present)
			}
		})
	}
}

type needsCase struct {
	kind Kind
	op   change.Op
	want bool
}

func TestNeedsMembers(t *testing.T) {
	cases := []needsCase{
		{Count, change.Insert, false}, {Count, change.Delete, false},
		{Sum, change.Insert, false}, {Sum, change.Delete, false},
		{Min, change.Insert, false}, {Min, change.Delete, true},
		{Max, change.Insert, false}, {Max, change.Delete, true},
		{DistinctCount, change.Insert, false}, {DistinctCount, change.Delete, true},
	}
	for _, tc := range cases {
		if got := NeedsMembers(tc.kind, tc.op); got != tc.want {
			t.Fatalf("NeedsMembers(%s,%s)=%v want %v", tc.kind, tc.op, got, tc.want)
		}
	}
}
