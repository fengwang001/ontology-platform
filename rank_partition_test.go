package ontology

import (
	"math"
	"reflect"
	"testing"
)

func TestPartitionIsolationAndOrder(t *testing.T) {
	rows := []Row{
		{Partition: strptr("b"), Value: 1, ID: "b1"},
		{Partition: strptr(""), Value: 9, ID: "e1"}, // empty key is legal
		{Partition: strptr("b"), Value: 1, ID: "b2"},
		{Partition: strptr("a"), Value: 1, ID: "a1"},
		{Partition: strptr("a"), Value: 1, ID: "a2"},
	}
	res := Rank(rows, Ascending)
	if res.Skipped != 0 {
		t.Fatalf("skipped=%d want 0", res.Skipped)
	}
	want := []struct {
		part  string
		id    string
		rank  int
		dense int
	}{
		{"", "e1", 1, 1},
		{"a", "a1", 1, 1},
		{"a", "a2", 1, 1}, // same value -> tie, RANK does not advance
		{"b", "b1", 1, 1},
		{"b", "b2", 1, 1},
	}
	if len(res.Rows) != len(want) {
		t.Fatalf("%d rows want %d", len(res.Rows), len(want))
	}
	for i, w := range want {
		r := res.Rows[i]
		if r.Partition != w.part || r.ID != w.id || r.Rank != w.rank || r.DenseRank != w.dense {
			t.Fatalf("row %d = %+v want part=%s id=%s rank=%d dense=%d",
				i, r, w.part, w.id, w.rank, w.dense)
		}
	}
}

func TestSkippedNilAndNaN(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: 1, ID: "ok1"},
		{Partition: nil, Value: 1, ID: "nil1"},
		{Partition: strptr("p"), Value: math.NaN(), ID: "nan1"},
		{Partition: nil, Value: math.NaN(), ID: "both"},
		{Partition: strptr("p"), Value: 2, ID: "ok2"},
	}
	res := Rank(rows, Ascending)
	if res.Skipped != 3 {
		t.Fatalf("skipped=%d want 3", res.Skipped)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("ranked %d rows want 2", len(res.Rows))
	}
	for _, r := range res.Rows {
		if r.ID == "nil1" || r.ID == "nan1" || r.ID == "both" {
			t.Fatalf("rejected id %q appeared in output", r.ID)
		}
	}
}

func TestInfinitiesAndSignedZero(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: math.Inf(1), ID: "pos"},
		{Partition: strptr("p"), Value: math.Inf(-1), ID: "neg"},
		{Partition: strptr("p"), Value: 0, ID: "zpos"},
		{Partition: strptr("p"), Value: math.Copysign(0, -1), ID: "zneg"},
		{Partition: strptr("p"), Value: 1, ID: "one"},
	}
	asc := Rank(rows, Ascending)
	wantIDs := []string{"neg", "zneg", "zpos", "one", "pos"}
	for i, id := range wantIDs {
		if asc.Rows[i].ID != id {
			t.Fatalf("asc position %d id=%s want %s", i, asc.Rows[i].ID, id)
		}
	}
	// The two zeros tie: ranks 2,2 then the next value is RANK 4.
	if asc.Rows[1].Rank != 2 || asc.Rows[2].Rank != 2 || asc.Rows[3].Rank != 4 {
		t.Fatalf("signed-zero tie ranks = %d %d %d, want 2 2 4",
			asc.Rows[1].Rank, asc.Rows[2].Rank, asc.Rows[3].Rank)
	}
	if asc.Rows[3].DenseRank != 3 {
		t.Fatalf("dense rank after zero tie = %d want 3", asc.Rows[3].DenseRank)
	}

	desc := Rank(rows, Descending)
	wantDesc := []string{"pos", "one", "zneg", "zpos", "neg"}
	for i, id := range wantDesc {
		if desc.Rows[i].ID != id {
			t.Fatalf("desc position %d id=%s want %s", i, desc.Rows[i].ID, id)
		}
	}
}

func TestInputNotMutated(t *testing.T) {
	rows := []Row{
		{Partition: strptr("b"), Value: 2, ID: "2"},
		{Partition: strptr("a"), Value: 1, ID: "1"},
		{Partition: strptr("a"), Value: 1, ID: "0"},
	}
	before := make([]Row, len(rows))
	copy(before, rows)

	res := Rank(rows, Descending)

	if !reflect.DeepEqual(rows, before) {
		t.Fatalf("input mutated:\nbefore=%+v\nafter =%+v", before, rows)
	}
	// Pointer targets must be unchanged too.
	for i := range rows {
		if *rows[i].Partition != *before[i].Partition {
			t.Fatalf("partition %d pointed value changed", i)
		}
	}
	// Output must not alias the input's backing array.
	res.Rows[0].ID = "mutated-output"
	for _, r := range rows {
		if r.ID == "mutated-output" {
			t.Fatal("output aliases input storage")
		}
	}
}

func TestComparisonBudget(t *testing.T) {
	for _, n := range []int{2, 4, 8, 16, 32, 64, 128, 256} {
		rows := make([]Row, n)
		for i := range rows {
			// Distinct values; ids assigned in reverse so keys are unsorted.
			rows[i] = Row{Partition: strptr("p"), Value: float64(n - i),
				ID: string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))}
		}
		res, comparisons := RankWithCount(rows, Ascending)
		bound := 10 * n * int(math.Ceil(math.Log2(float64(n+1))))
		if comparisons > bound {
			t.Fatalf("n=%d comparisons=%d exceeds bound %d", n, comparisons, bound)
		}
		if len(res.Rows) != n {
			t.Fatalf("n=%d produced %d rows", n, len(res.Rows))
		}
	}
}
