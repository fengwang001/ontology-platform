package combine_test

import (
	"fmt"
	"testing"

	"ontology/combine"
	"ontology/fanout"
	"ontology/shard"
)

func ok(id string, recs ...shard.Record) fanout.Outcome {
	return fanout.Outcome{ShardID: id, Status: fanout.StatusOK, Records: recs}
}

func bad(id string, st fanout.Status, bound float64) fanout.Outcome {
	return fanout.Outcome{ShardID: id, Status: st, Bound: bound}
}

func res(outs ...fanout.Outcome) *fanout.Result { return &fanout.Result{Outcomes: outs} }

func TestAll(t *testing.T) {
	cases := []struct {
		name     string
		r        *fanout.Result
		k        int
		count    int64
		sum      float64
		min, max float64
		topIDs   []string
	}{
		{"all-success", res(
			ok("a", shard.Record{ID: "a1", V: 5}, shard.Record{ID: "a2", V: 1}),
			ok("b", shard.Record{ID: "b1", V: 3})),
			2, 3, 9, 1, 5, []string{"a1", "b1"}},
		{"one-missing", res(
			ok("a", shard.Record{ID: "a1", V: 5}, shard.Record{ID: "a2", V: 1}),
			bad("b", fanout.StatusTimeout, 100)),
			2, 2, 6, 1, 5, []string{"a1", "a2"}},
		{"half-missing", res(
			ok("a", shard.Record{ID: "a1", V: 5}),
			bad("b", fanout.StatusTimeout, 100),
			ok("c", shard.Record{ID: "c1", V: 2}),
			bad("d", fanout.StatusCorrupt, 100)),
			3, 2, 7, 2, 5, []string{"a1", "c1"}},
		{"tie-break-by-id", res(
			ok("a", shard.Record{ID: "z", V: 4}, shard.Record{ID: "m", V: 4}, shard.Record{ID: "a", V: 4})),
			3, 3, 12, 4, 4, []string{"a", "m", "z"}},
		{"k-exceeds-total", res(
			ok("a", shard.Record{ID: "a1", V: 2}, shard.Record{ID: "a2", V: 1})),
			10, 2, 3, 1, 2, []string{"a1", "a2"}},
		{"empty-success", res(ok("a"), ok("b")),
			3, 0, 0, 0, 0, nil},
	}
	for _, tc := range cases {
		v := combine.All(tc.r, tc.k)
		if v.Count != tc.count || v.Sum != tc.sum {
			t.Errorf("%s: Count=%d Sum=%v want %d/%v", tc.name, v.Count, v.Sum, tc.count, tc.sum)
		}
		if tc.count > 0 && (v.Min != tc.min || v.Max != tc.max) {
			t.Errorf("%s: Min=%v Max=%v want %v/%v", tc.name, v.Min, v.Max, tc.min, tc.max)
		}
		if len(v.TopK) != len(tc.topIDs) {
			t.Fatalf("%s: TopK len=%d want %d", tc.name, len(v.TopK), len(tc.topIDs))
		}
		for i, id := range tc.topIDs {
			if v.TopK[i].ID != id {
				t.Errorf("%s: TopK[%d]=%q want %q", tc.name, i, v.TopK[i].ID, id)
			}
		}
	}
}

func TestMissingAndBounds(t *testing.T) {
	r := res(
		ok("b", shard.Record{ID: "b1", V: 1}),
		bad("a", fanout.StatusTimeout, 7),
		bad("c", fanout.StatusError, 3),
	)
	miss := combine.MissingShardIDs(r)
	if fmt.Sprint(miss) != "[a c]" {
		t.Errorf("missing=%v want [a c] (sorted)", miss)
	}
	if b := combine.MissingBoundSum(r); b != 10 {
		t.Errorf("bound sum=%v want 10", b)
	}
	if combine.AllOK(r) {
		t.Error("AllOK=true want false")
	}
	if !combine.AllOK(res(ok("x"))) {
		t.Error("AllOK=false want true")
	}
}
