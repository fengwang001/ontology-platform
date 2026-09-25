package rank

import (
	"math/rand"
	"testing"
)

// baseRows returns a fresh slice of rows with several ties, so that
// ROW_NUMBER among tied rows is observable.
func baseRows() []Row {
	p := "p"
	vals := []float64{20, 10, 20, 30, 20, 10, 5, 5}
	ids := []string{"r3", "r1", "r5", "r8", "r2", "r7", "r4", "r6"}
	rows := make([]Row, len(vals))
	for i := range vals {
		rows[i] = Row{Partition: &p, Value: vals[i], ID: ids[i]}
	}
	return rows
}

func sameRankedRows(a, b []RankedRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Row.ID != b[i].Row.ID ||
			a[i].Row.Value != b[i].Row.Value ||
			*a[i].Row.Partition != *b[i].Row.Partition ||
			a[i].RowNumber != b[i].RowNumber ||
			a[i].Rank != b[i].Rank ||
			a[i].DenseRank != b[i].DenseRank {
			return false
		}
	}
	return true
}

// Shuffling the input must never change the output, including the
// ROW_NUMBER column. This runs well over 20 distinct permutations.
func TestDeterministicUnderShuffle(t *testing.T) {
	want := Rank(baseRows(), Config{}).Rows

	const trials = 40
	for seed := int64(0); seed < trials; seed++ {
		rows := baseRows()
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(rows), func(i, j int) {
			rows[i], rows[j] = rows[j], rows[i]
		})
		got := Rank(rows, Config{}).Rows
		if !sameRankedRows(want, got) {
			t.Fatalf("seed %d: output differs from unshuffled output\ngot:  %v\nwant: %v", seed, got, want)
		}
	}
}

// Same requirement under descending order.
func TestDeterministicUnderShuffleDescending(t *testing.T) {
	cfg := Config{Descending: true}
	want := Rank(baseRows(), cfg).Rows

	const trials = 40
	for seed := int64(0); seed < trials; seed++ {
		rows := baseRows()
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(rows), func(i, j int) {
			rows[i], rows[j] = rows[j], rows[i]
		})
		got := Rank(rows, cfg).Rows
		if !sameRankedRows(want, got) {
			t.Fatalf("seed %d: descending output differs", seed)
		}
	}
}
