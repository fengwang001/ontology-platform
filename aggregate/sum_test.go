package aggregate

import (
	"errors"
	"math"
	"testing"
)

func TestInt64OverflowIsNamedError(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "big", "v": int64(math.MaxInt64)})
	agg.Add(map[string]any{"k": "big", "v": int64(1)})
	agg.Add(map[string]any{"k": "ok", "v": int64(2)})

	res, err := agg.Snapshot()
	if err == nil {
		t.Fatal("expected overflow error")
	}
	var oe *OverflowError
	if !errors.As(err, &oe) {
		t.Fatalf("expected *OverflowError, got %T", err)
	}
	if len(oe.Groups) != 1 || oe.Groups[0].Columns[0].Value != "big" {
		t.Fatalf("overflow must name the offending group, got %v", oe.Groups)
	}
	var overflowing *Result
	for i := range res {
		if res[i].Key.Columns[0].Value == "big" {
			overflowing = &res[i]
		}
	}
	if overflowing == nil || !overflowing.Overflow || overflowing.SumValid {
		t.Fatalf("overflow group flagged incorrectly: %+v", overflowing)
	}
}

func TestInt64SumExact(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "g", "v": int64(40)})
	agg.Add(map[string]any{"k": "g", "v": int64(2)})
	res, err := agg.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r := res[0]
	if r.IsFloat || !r.SumValid || r.IntSum != 42 {
		t.Fatalf("want exact int64 sum 42, got %+v", r)
	}
}

func TestMixedIntFloatGivesFloat(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "g", "v": int64(1)})
	agg.Add(map[string]any{"k": "g", "v": 2.5})
	res, _ := agg.Snapshot()
	r := res[0]
	if !r.IsFloat || !r.SumValid || r.FloatSum != 3.5 {
		t.Fatalf("want float 3.5, got %+v", r)
	}
}

func TestUnsummableRowsCountAndSkip(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	rows := []map[string]any{
		{"k": "g", "v": int64(3)},
		{"k": "g", "v": "nope"}, // string
		{"k": "g", "v": true},   // bool
		{"k": "g", "v": nil},    // nil
		{"k": "g"},              // missing
		{"k": "g", "v": int64(4)},
	}
	for _, r := range rows {
		agg.Add(r)
	}
	res, _ := agg.Snapshot()
	r := res[0]
	if r.Count != 6 || r.Skipped != 4 {
		t.Fatalf("want Count=6 Skipped=4, got Count=%d Skipped=%d", r.Count, r.Skipped)
	}
	if !r.SumValid || r.IntSum != 7 {
		t.Fatalf("sum must ignore skipped rows, got %+v", r)
	}
}

func TestNaNSkippedDoesNotPoison(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "g", "v": 3.0})
	agg.Add(map[string]any{"k": "g", "v": math.NaN()})
	agg.Add(map[string]any{"k": "g", "v": 4.0})
	res, _ := agg.Snapshot()
	r := res[0]
	if r.Count != 3 || r.Skipped != 1 {
		t.Fatalf("want Count=3 Skipped=1, got %d/%d", r.Count, r.Skipped)
	}
	if math.IsNaN(r.FloatSum) || r.FloatSum != 7.0 {
		t.Fatalf("NaN must not poison sum, got %v", r.FloatSum)
	}
}

func TestInfinitiesParticipate(t *testing.T) {
	agg := NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"k": "p", "v": 1.0})
	agg.Add(map[string]any{"k": "p", "v": math.Inf(1)})
	agg.Add(map[string]any{"k": "n", "v": -2.0})
	agg.Add(map[string]any{"k": "n", "v": math.Inf(-1)})
	res, _ := agg.Snapshot()
	byName := map[string]Result{}
	for _, r := range res {
		byName[r.Key.Columns[0].Value.(string)] = r
	}
	if byName["p"].FloatSum != math.Inf(1) {
		t.Errorf("+Inf group wrong: %v", byName["p"].FloatSum)
	}
	if byName["n"].FloatSum != math.Inf(-1) {
		t.Errorf("-Inf group wrong: %v", byName["n"].FloatSum)
	}
}
