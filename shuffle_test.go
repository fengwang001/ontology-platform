package ontology

import (
	"math/rand"
	"reflect"
	"testing"
)

// Tie rows in their natural input order do not have ascending IDs, so any
// implementation using the input index instead of the ID for tiebreaking
// would surface here.
func shuffledTieRows() []Row {
	return []Row{
		{Partition: ptr("p"), Value: 20, ID: "id9"},
		{Partition: ptr("p"), Value: 10, ID: "id2"},
		{Partition: ptr("p"), Value: 20, ID: "id5"},
		{Partition: ptr("p"), Value: 30, ID: "id7"},
		{Partition: ptr("p"), Value: 20, ID: "id1"},
		{Partition: ptr("p"), Value: 10, ID: "id4"},
	}
}

func expectedShuffledRanks() []expectedRank {
	// Ascending values; inside each tie IDs must come out ascending.
	return []expectedRank{
		{"id2", 1, 1, 1}, // value 10
		{"id4", 2, 1, 1}, // value 10
		{"id1", 3, 3, 2}, // value 20
		{"id5", 4, 3, 2},
		{"id9", 5, 3, 2},
		{"id7", 6, 6, 3}, // value 30
	}
}

func TestTieOrderByIDNotInputIndex(t *testing.T) {
	res := Rank(shuffledTieRows(), Options{})
	assertRanks(t, res.Rows, expectedShuffledRanks())
}

func TestOutputStableUnderPermutations(t *testing.T) {
	const permutations = 25
	base := shuffledTieRows()
	reference := Rank(base, Options{}).Rows
	assertRanks(t, reference, expectedShuffledRanks())

	for seed := int64(0); seed < permutations; seed++ {
		rng := rand.New(rand.NewSource(seed + 1))
		shuffled := make([]Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		got := Rank(shuffled, Options{}).Rows
		if !reflect.DeepEqual(got, reference) {
			t.Fatalf("seed %d: output differs from reference\n got=%v\nwant=%v",
				seed, got, reference)
		}
	}
}

func TestDescendingTieOrderByIDAscending(t *testing.T) {
	res := Rank(shuffledTieRows(), Options{Descending: true})
	assertRanks(t, res.Rows, []expectedRank{
		{"id7", 1, 1, 1}, // value 30
		{"id1", 2, 2, 2}, // value 20, IDs ascending inside the tie
		{"id5", 3, 2, 2},
		{"id9", 4, 2, 2},
		{"id2", 5, 5, 3}, // value 10
		{"id4", 6, 5, 3},
	})
}
