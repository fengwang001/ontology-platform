package rank_test

import (
	"math/rand"
	"testing"

	"ontology/rank"
)

// The same logical input, shuffled many times, must produce byte-for-
// byte identical output, including ROW_NUMBER within ties. Tie order
// comes from row IDs, never from slice positions or map iteration.
func TestDeterminismAcrossPermutations(t *testing.T) {
	base := makeRows("p",
		[]string{"r0", "r1", "r2", "r3", "r4", "r5", "r6"},
		[]float64{20, 10, 20, 30, 20, 10, 40})

	reference := rank.Compute(base, rank.Options{})
	refP := onlyPartition(t, reference)
	wantIDs := ids(refP)
	wantTriples := triples(refP)

	// Ties must be ordered by ID ascending, not input position.
	if !equalStrings(wantIDs, []string{"r1", "r5", "r0", "r2", "r4", "r3", "r6"}) {
		t.Fatalf("reference id order = %v", wantIDs)
	}

	rng := rand.New(rand.NewSource(42))
	const permutations = 25 // >= 20 as required
	for trial := 0; trial < permutations; trial++ {
		shuffled := make([]rank.Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		got := onlyPartition(t, rank.Compute(shuffled, rank.Options{}))
		if gotIDs := ids(got); !equalStrings(gotIDs, wantIDs) {
			t.Fatalf("trial %d: id order = %v, want %v", trial, gotIDs, wantIDs)
		}
		if gotTriples := triples(got); !equalTriples(gotTriples, wantTriples) {
			t.Fatalf("trial %d: triples = %v, want %v", trial, gotTriples, wantTriples)
		}
	}
}

// Determinism must also hold across many partitions, where map
// iteration order could otherwise leak into the output.
func TestDeterminismAcrossPartitions(t *testing.T) {
	var base []rank.Row
	for _, key := range []string{"zeta", "alpha", "", "mid"} {
		base = append(base, makeRows(key,
			[]string{key + "-a", key + "-b", key + "-c"},
			[]float64{7, 7, 3})...)
	}

	reference := rank.Compute(base, rank.Options{})
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		shuffled := make([]rank.Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := rank.Compute(shuffled, rank.Options{})
		if len(got.Partitions) != len(reference.Partitions) {
			t.Fatalf("trial %d: partition count differs", trial)
		}
		for pi := range reference.Partitions {
			ref, g := reference.Partitions[pi], got.Partitions[pi]
			if ref.Key != g.Key {
				t.Fatalf("trial %d: partition %d key = %q, want %q", trial, pi, g.Key, ref.Key)
			}
			if !equalTriples(triples(ref), triples(g)) || !equalStrings(ids(ref), ids(g)) {
				t.Fatalf("trial %d: partition %q output differs", trial, ref.Key)
			}
		}
	}
}
