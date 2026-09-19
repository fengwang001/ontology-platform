package ontology

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// Aggregator-level: non-summable rows still count, and are skipped.
func TestAggregatorSkippedRows(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "a", "v": int64(10)})
	agg.Add(map[string]any{"g": "a", "v": "bad"})
	agg.Add(map[string]any{"g": "a", "v": true})
	agg.Add(map[string]any{"g": "a"}) // sum attribute absent
	agg.Add(map[string]any{"g": "a", "v": math.NaN()})

	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d groups, want 1", len(results))
	}
	r := results[0]
	if r.Count != 5 {
		t.Fatalf("Count = %d, want 5", r.Count)
	}
	if r.Skipped != 4 {
		t.Fatalf("Skipped = %d, want 4", r.Skipped)
	}
	if !r.IsInt || r.IntSum != 10 {
		t.Fatalf("sum = %+v, want int64 10", r)
	}
}

// Overflow is a decidable error that names the offending group.
func TestAggregatorOverflowErrorIdentifiesGroup(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "big", "v": int64(math.MaxInt64)})
	agg.Add(map[string]any{"g": "big", "v": int64(1)})
	agg.Add(map[string]any{"g": "small", "v": int64(7)})

	results, err := agg.Snapshot()
	if err == nil {
		t.Fatal("expected overflow error")
	}
	var ovf *OverflowError
	if !errors.As(err, &ovf) {
		t.Fatalf("error %v is not an *OverflowError", err)
	}
	if len(ovf.Groups) != 1 || KeyString(ovf.Groups[0]) != "(big)" {
		t.Fatalf("overflow groups = %v, want [(big)]", ovf.Groups)
	}
	// Non-overflowing groups are still returned.
	if len(results) != 1 || results[0].IntSum != 7 {
		t.Fatalf("results = %+v, want the small group only", results)
	}
}

// Same input, repeatedly aggregated in different row orders, must give
// an element-wise identical output sequence.
func TestSnapshotDeterministicAcrossRuns(t *testing.T) {
	base := []map[string]any{
		{"g": "us", "v": 0.1},
		{"g": "us", "v": 1e16},
		{"g": "us", "v": -1e16},
		{"g": "eu", "v": int64(3)},
		{"g": nil, "v": 2.5},
		{"v": int64(1)},
		{"g": "", "v": int64(2)},
		{"g": "us", "v": 0.2},
	}
	snapshotOf := func(rows []map[string]any) []GroupResult {
		agg := NewAggregator([]string{"g"}, "v")
		for _, row := range rows {
			agg.Add(row)
		}
		res, err := agg.Snapshot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return res
	}

	want := snapshotOf(base)
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 50; round++ {
		rows := make([]map[string]any, len(base))
		copy(rows, base)
		rng.Shuffle(len(rows), func(i, j int) {
			rows[i], rows[j] = rows[j], rows[i]
		})
		got := snapshotOf(rows)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: output differs:\n got %+v\nwant %+v",
				round, got, want)
		}
		// Bit-level check of every float sum.
		for i := range got {
			if math.Float64bits(got[i].FloatSum) !=
				math.Float64bits(want[i].FloatSum) {
				t.Fatalf("round %d group %d: float bits differ", round, i)
			}
		}
	}
}

// Snapshot results are independent copies: mutating them must not
// affect the aggregator's internal state.
func TestSnapshotIsolation(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "a", "v": int64(1)})

	first, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first[0].Count = 999
	first[0].Key[0] = KeyPart{Kind: PartNil}

	second, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second[0].Count != 1 || second[0].Key[0].Kind != PartValue {
		t.Fatalf("snapshot mutation leaked into state: %+v", second[0])
	}
}

// Mixed int64/float64 in one group yields a float64 sum.
func TestAggregatorMixedSumIsFloat(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "a", "v": int64(2)})
	agg.Add(map[string]any{"g": "a", "v": 0.5})
	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := results[0]
	if r.IsInt {
		t.Fatal("mixed group must not be IsInt")
	}
	if r.FloatSum != 2.5 || r.Sum() != 2.5 {
		t.Fatalf("FloatSum = %v, want 2.5", r.FloatSum)
	}
}
