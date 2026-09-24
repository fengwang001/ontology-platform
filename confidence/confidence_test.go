package confidence

import (
	"context"
	"testing"

	"ontology/combine"
	"ontology/shard"
)

func rec(id string, v float64) shard.Record { return shard.Record{ID: id, Value: v} }

func ok(id string, bound float64, recs ...shard.Record) shard.Result {
	return shard.Result{ShardID: id, Claimed: len(recs), Records: recs, Bound: bound}
}

func gone(id string, bound float64) shard.Result {
	return shard.Result{ShardID: id, Bound: bound, Err: context.DeadlineExceeded}
}

// scenarios returns all-success, one-missing and half-missing merges.
func scenarios() map[string]combine.Merged {
	out := map[string]combine.Merged{}
	base := []shard.Result{
		ok("a", 10, rec("a1", 8), rec("a2", 3)),
		ok("b", 10, rec("b1", 6), rec("b2", 5)),
		ok("c", 10, rec("c1", 4)),
		ok("d", 10, rec("d1", 2)),
	}
	m, _ := combine.Merge(base, 3)
	out["all success"] = m
	m, _ = combine.Merge(append(append([]shard.Result{}, base[:3]...), gone("d", 1)), 3)
	out["one missing"] = m
	m, _ = combine.Merge(append(append([]shard.Result{}, base[:2]...),
		gone("c", 1), gone("d", 1)), 3)
	out["half missing"] = m
	return out
}

func TestAssessMatrix(t *testing.T) {
	s := scenarios()
	cases := []struct {
		agg      combine.Agg
		scenario string
		wantDir  Direction
		wantText string
	}{
		{combine.AggCount, "all success", Exact, "6"},
		{combine.AggSum, "all success", Exact, "28"},
		{combine.AggMin, "all success", Exact, "2"},
		{combine.AggMax, "all success", Exact, "8"},
		{combine.AggTopK, "all success", Exact, "trusted prefix 3/3"},
		{combine.AggCount, "one missing", AtLeast, ">= 5"},
		{combine.AggSum, "one missing", AtLeast, ">= 26"},
		{combine.AggMin, "one missing", AtMost, "<= 3"},
		{combine.AggMax, "one missing", AtLeast, ">= 8"},
		{combine.AggTopK, "one missing", Uncertain, "trusted prefix 3/3"},
		{combine.AggCount, "half missing", AtLeast, ">= 4"},
		{combine.AggSum, "half missing", AtLeast, ">= 22"},
		{combine.AggMin, "half missing", AtMost, "<= 3"},
		{combine.AggMax, "half missing", AtLeast, ">= 8"},
		{combine.AggTopK, "half missing", Uncertain, "trusted prefix 3/3"},
	}
	for _, tc := range cases {
		got := Assess(tc.agg, s[tc.scenario])
		if got.Direction != tc.wantDir || got.Text != tc.wantText {
			t.Errorf("agg %d / %s: got (%d, %q), want (%d, %q)",
				tc.agg, tc.scenario, got.Direction, got.Text, tc.wantDir, tc.wantText)
		}
	}
}

func TestTopKTrustedPrefix(t *testing.T) {
	results := []shard.Result{
		ok("a", 10, rec("a1", 8), rec("a2", 3)),
		ok("b", 10, rec("b1", 6), rec("b2", 5)),
	}
	cases := []struct {
		name         string
		missingBound float64
		wantPrefix   int
	}{
		{"small missing bound keeps full K", 1, 3},
		{"mid bound keeps only certain entries", 5, 2},
		{"large missing bound yields zero", 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := append(append([]shard.Result{}, results...), gone("x", tc.missingBound))
			m, _ := combine.Merge(res, 3)
			if got := Assess(combine.AggTopK, m).TrustedPrefix; got != tc.wantPrefix {
				t.Fatalf("trusted prefix = %d, want %d", got, tc.wantPrefix)
			}
		})
	}
}

func TestEmptySuccessMinMaxUncertain(t *testing.T) {
	m, _ := combine.Merge([]shard.Result{ok("a", 1), gone("b", 1)}, 3)
	for _, agg := range []combine.Agg{combine.AggMin, combine.AggMax} {
		if got := Assess(agg, m); got.Direction != Uncertain {
			t.Fatalf("agg %d: got dir %d, want Uncertain", agg, got.Direction)
		}
	}
	if got := Assess(combine.AggCount, m); got.Direction != AtLeast || got.Text != ">= 0" {
		t.Fatalf("count on empty partial: %+v", got)
	}
}
