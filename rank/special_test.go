package rank_test

import (
	"math"
	"testing"

	"ontology/rank"
)

// NaN sort values are rejected and counted; they never reach the
// comparator and never disturb the surviving rows' ranks.
func TestNaNSkipped(t *testing.T) {
	rows := makeRows("p", []string{"a", "b", "c"}, []float64{1, 2, 3})
	rows = append(rows,
		rank.Row{Partition: strPtr("p"), Value: math.NaN(), ID: "nan1"},
		rank.Row{Partition: strPtr("q"), Value: math.NaN(), ID: "nan2"},
	)

	res := rank.Compute(rows, rank.Options{})
	if res.Skipped != 2 || res.SkippedNaN != 2 {
		t.Fatalf("skipped = %d (NaN %d), want 2 (2)", res.Skipped, res.SkippedNaN)
	}
	if res.SkippedNilPartition != 0 {
		t.Fatalf("skipped nil = %d, want 0", res.SkippedNilPartition)
	}
	if len(res.Partitions) != 1 {
		t.Fatalf("partitions = %d, want 1 (no NaN partition leaked)", len(res.Partitions))
	}
	assertTriples(t, res.Partitions[0], [][3]int{{1, 1, 1}, {2, 2, 2}, {3, 3, 3}})
}

// +0.0 and -0.0 are equal: they tie, share RANK/DENSE_RANK, and are
// ordered between themselves by row ID ascending.
func TestSignedZeroTies(t *testing.T) {
	rows := makeRows("p",
		[]string{"pos", "neg", "one"},
		[]float64{0.0, math.Copysign(0, -1), 1.0})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1}, // neg (ID "neg" < "pos")
		{2, 1, 1}, // pos
		{3, 3, 2}, // one
	})
	if got := ids(p); !equalStrings(got, []string{"neg", "pos", "one"}) {
		t.Errorf("id order = %v, want [neg pos one]", got)
	}
}

// +/-Inf are legal sort values and land at the two ends.
func TestInfinitiesRankAtEnds(t *testing.T) {
	rows := makeRows("p",
		[]string{"mid", "hi", "lo", "hi2"},
		[]float64{0, math.Inf(1), math.Inf(-1), math.Inf(1)})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1}, // -Inf
		{2, 2, 2}, // 0
		{3, 3, 3}, // +Inf (hi)
		{4, 3, 3}, // +Inf (hi2), tied
	})
	if got := ids(p); !equalStrings(got, []string{"lo", "mid", "hi", "hi2"}) {
		t.Errorf("id order = %v, want [lo mid hi hi2]", got)
	}

	// Descending flips the ends but keeps tie semantics.
	d := onlyPartition(t, rank.Compute(rows, rank.Options{Descending: true}))
	assertTriples(t, d, [][3]int{
		{1, 1, 1}, // +Inf (hi)
		{2, 1, 1}, // +Inf (hi2)
		{3, 3, 2}, // 0
		{4, 4, 3}, // -Inf
	})
}
