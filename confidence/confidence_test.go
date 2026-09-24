package confidence_test

import (
	"math"
	"testing"

	"ontology/combine"
	"ontology/confidence"
	"ontology/fanout"
	"ontology/shard"
)

func ok(id string, recs ...shard.Record) fanout.Outcome {
	return fanout.Outcome{ShardID: id, Status: fanout.StatusOK, Records: recs}
}

func miss(id string, bound float64) fanout.Outcome {
	return fanout.Outcome{ShardID: id, Status: fanout.StatusTimeout, Bound: bound}
}

func labels(r *fanout.Result, k int) map[combine.Kind]confidence.Label {
	return confidence.Assess(r, combine.All(r, k))
}

func TestAssessGrid(t *testing.T) {
	a := ok("a", shard.Record{ID: "a1", V: 5}, shard.Record{ID: "a2", V: 1})
	b := ok("b", shard.Record{ID: "b1", V: 3})
	c := ok("c", shard.Record{ID: "c1", V: 2})
	cases := []struct {
		name   string
		r      *fanout.Result
		k      int
		texts  map[combine.Kind]string
		prefix int
	}{
		{"all-success", &fanout.Result{Outcomes: []fanout.Outcome{a, b}}, 2,
			map[combine.Kind]string{
				combine.Count: "exact Count=3", combine.Sum: "exact Sum=9",
				combine.Min: "exact Min=1", combine.Max: "exact Max=5",
				combine.TopK: "exact TopK[2]"}, 2},
		{"one-missing-small-bound", &fanout.Result{Outcomes: []fanout.Outcome{a, b, miss("z", 2)}}, 2,
			map[combine.Kind]string{
				combine.Count: "Count >= 3", combine.Sum: "Sum >= 9",
				combine.Min: "Min <= 1", combine.Max: "Max >= 5",
				combine.TopK: "TopK membership certain (prefix=2), order may shift"}, 2},
		{"half-missing-big-bound", &fanout.Result{Outcomes: []fanout.Outcome{a, c, miss("y", 100), miss("z", 100)}}, 2,
			map[combine.Kind]string{
				combine.Count: "Count >= 3", combine.Sum: "Sum >= 8",
				combine.Min: "Min <= 1", combine.Max: "Max >= 5",
				combine.TopK: "TopK guaranteed prefix=0"}, 0},
	}
	for _, tc := range cases {
		ls := labels(tc.r, tc.k)
		for kind, want := range tc.texts {
			if ls[kind].Text != want {
				t.Errorf("%s %v: text=%q want %q", tc.name, kind, ls[kind].Text, want)
			}
		}
		if ls[combine.TopK].Prefix != tc.prefix {
			t.Errorf("%s: TopK prefix=%d want %d", tc.name, ls[combine.TopK].Prefix, tc.prefix)
		}
	}
}

func TestTopKPrefixBounds(t *testing.T) {
	recs := []shard.Record{{ID: "p", V: 10}, {ID: "q", V: 8}, {ID: "r", V: 6}}
	small := &fanout.Result{Outcomes: []fanout.Outcome{ok("a", recs...), miss("z", 5)}}
	if p := labels(small, 3)[combine.TopK].Prefix; p != 3 {
		t.Errorf("small missing bound: prefix=%d want K=3", p)
	}
	big := &fanout.Result{Outcomes: []fanout.Outcome{ok("a", recs...), miss("z", 1000)}}
	if p := labels(big, 3)[combine.TopK].Prefix; p != 0 {
		t.Errorf("big missing bound: prefix=%d want 0", p)
	}
}

func TestIntervals(t *testing.T) {
	r := &fanout.Result{Outcomes: []fanout.Outcome{
		ok("a", shard.Record{ID: "a1", V: 5}), miss("z", 1)}}
	ls := labels(r, 1)
	if ls[combine.Count].Lower != 1 || !math.IsInf(ls[combine.Count].Upper, 1) {
		t.Errorf("Count interval [%v,%v] want [1,+inf]", ls[combine.Count].Lower, ls[combine.Count].Upper)
	}
	if !math.IsInf(ls[combine.Min].Lower, -1) || ls[combine.Min].Upper != 5 {
		t.Errorf("Min interval [%v,%v] want [-inf,5]", ls[combine.Min].Lower, ls[combine.Min].Upper)
	}
	if ls[combine.Max].Lower != 5 || !math.IsInf(ls[combine.Max].Upper, 1) {
		t.Errorf("Max interval [%v,%v] want [5,+inf]", ls[combine.Max].Lower, ls[combine.Max].Upper)
	}
	if !ls[combine.Count].Exact && ls[combine.Min].Exact {
		t.Error("Min must not be exact when a shard is missing")
	}
}
