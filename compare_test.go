package ontology

import (
	"math"
	"reflect"
	"testing"
)

func nan() float64     { return math.NaN() }
func posZero() float64 { return 0 }
func negZero() float64 { return math.Copysign(0, -1) }
func inf(sign int) float64 {
	return math.Inf(sign)
}

func TestSignedZeroEquality(t *testing.T) {
	if !equalValues(posZero(), negZero()) {
		t.Fatal("+0.0 and -0.0 must compare equal")
	}
	if equalValues(1, 2) {
		t.Fatal("distinct values must not compare equal")
	}
}

func TestComparisonCountIsO_nlogn(t *testing.T) {
	for _, n := range []int{2, 4, 8, 32, 128, 1000} {
		rows := make([]Row, n)
		key := "p"
		for i := range rows {
			rows[i] = Row{Partition: &key, Value: float64((i * 7) % n), ID: itoa(i + 1)}
		}
		rep := Rank(rows, Asc)
		bound := maxComparisons(n)
		if rep.Comparisons > bound {
			t.Fatalf("n=%d comparisons=%d exceeds bound %d",
				n, rep.Comparisons, bound)
		}
	}
}

func TestInputNotMutatedAndResultFresh(t *testing.T) {
	key := "p"
	rows := []Row{
		{Partition: &key, Value: 2, ID: "b"},
		{Partition: &key, Value: 1, ID: "a"},
		{Partition: &key, Value: 2, ID: "c"},
	}
	before := append([]Row(nil), rows...)
	rep := Rank(rows, Asc)

	if !reflect.DeepEqual(rows, before) {
		t.Fatalf("input mutated:\nbefore %#v\nafter  %#v", before, rows)
	}
	for i := range rows {
		if rows[i].Partition != before[i].Partition {
			t.Fatal("partition pointer must be untouched")
		}
	}
	if len(rep.Rows) > 0 && &rep.Rows[0] == &rows[0] {
		t.Fatal("result must not alias input storage")
	}
}
