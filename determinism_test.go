package ontology

import (
	"math/rand"
	"testing"
)

// rowsEqual compares two ranked outputs field by field.
func rowsEqual(a, b []RankedRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDeterminism shuffles the same tied rows many times and requires
// identical output, including ROW_NUMBER. Tie order must come from
// row IDs, never from input position or map iteration order.
func TestDeterminism_ShuffledInputs(t *testing.T) {
	base := []Row{
		{Partition: StringPtr("p"), Value: 20, ID: "a"},
		{Partition: StringPtr("p"), Value: 10, ID: "b"},
		{Partition: StringPtr("p"), Value: 20, ID: "c"},
		{Partition: StringPtr("p"), Value: 30, ID: "d"},
		{Partition: StringPtr("p"), Value: 20, ID: "e"},
		{Partition: StringPtr("p"), Value: 10, ID: "f"},
	}
	want := Compute(base, Options{})

	const trials = 25
	for seed := int64(0); seed < trials; seed++ {
		perm := rand.New(rand.NewSource(seed)).Perm(len(base))
		shuffled := make([]Row, len(base))
		for i, j := range perm {
			shuffled[i] = base[j]
		}
		got := Compute(shuffled, Options{})
		if !rowsEqual(got.Rows, want.Rows) {
			t.Fatalf("seed %d: output differs across permutations\ngot:  %v\nwant: %v",
				seed, got.Rows, want.Rows)
		}
	}

	// Within the value-20 tie, ROW_NUMBER must follow ID order a,c,e.
	var tieIDs []string
	for _, r := range want.Rows {
		if r.Row.Value == 20 {
			tieIDs = append(tieIDs, r.Row.ID)
		}
	}
	wantIDs := []string{"a", "c", "e"}
	for i, id := range wantIDs {
		if tieIDs[i] != id {
			t.Fatalf("tie order = %v, want %v", tieIDs, wantIDs)
		}
	}
}
