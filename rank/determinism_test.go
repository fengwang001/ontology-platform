package rank

import (
	"math/rand"
	"reflect"
	"testing"
)

// shuffled returns a permutation of rows using a seeded Fisher-Yates
// shuffle, so the test itself is reproducible.
func shuffled(rows []Row, seed int64) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

// TestDeterministicAcrossPermutations shuffles one row set (with
// ties across two partitions) many times and requires byte-identical
// output, including RowNumber, for every permutation.
func TestDeterministicAcrossPermutations(t *testing.T) {
	base := []Row{
		{Partition: strPtr("b"), Value: 20, ID: 5},
		{Partition: strPtr("a"), Value: 20, ID: 3},
		{Partition: strPtr("a"), Value: 10, ID: 1},
		{Partition: strPtr("b"), Value: 20, ID: 4},
		{Partition: strPtr("a"), Value: 20, ID: 2},
		{Partition: strPtr("b"), Value: 30, ID: 6},
		{Partition: strPtr("a"), Value: 20, ID: 7},
		{Partition: strPtr("b"), Value: 10, ID: 8},
	}

	want := Compute(base, Options{})
	const permutations = 20
	seen := make(map[string]bool)
	for seed := int64(1); seed <= permutations; seed++ {
		perm := shuffled(base, seed)
		key := ""
		for _, r := range perm {
			key += string(rune(r.ID)) // order fingerprint
		}
		seen[key] = true
		got := Compute(perm, Options{})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: output differs\n got: %v\nwant: %v", seed, got, want)
		}
	}
	if len(seen) < permutations/2 {
		t.Fatalf("shuffles produced only %d distinct orders, test is weak", len(seen))
	}
}

// TestTieOrderByIDAscending pins the tie-break rule: within equal
// values, RowNumber follows ID ascending, not input position.
func TestTieOrderByIDAscending(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 7, ID: 30},
		{Partition: strPtr("p"), Value: 7, ID: 10},
		{Partition: strPtr("p"), Value: 7, ID: 20},
	}
	res := Compute(rows, Options{})
	wantIDs := []int64{10, 20, 30}
	for i, id := range wantIDs {
		rr := res.Rows[i]
		if rr.Row.ID != id || rr.RowNumber != i+1 {
			t.Errorf("row %d: got (id=%d, rn=%d), want (id=%d, rn=%d)",
				i, rr.Row.ID, rr.RowNumber, id, i+1)
		}
	}
}
