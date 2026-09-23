package agg

import "testing"

func TestInsertAndPolicy(t *testing.T) {
	cases := []struct {
		name    string
		a       Aggregator
		vals    []float64
		want    float64
		needMem bool
	}{
		{"count", NewCount(), []float64{1, 2, 3}, 3, false},
		{"sum", NewSum(), []float64{1.5, -0.5, 2}, 3, false},
		{"min", NewMin(), []float64{3, 1, 2}, 1, true},
		{"max", NewMax(), []float64{3, 7, 2}, 7, true},
		{"distinct", NewDistinctCount(), []float64{1, 1, 2, 3, 3}, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range tc.vals {
				tc.a.Insert(v)
			}
			if tc.a.Value() != tc.want {
				t.Fatalf("value=%v want %v", tc.a.Value(), tc.want)
			}
			if tc.a.NeedsMembersOnDelete() != tc.needMem {
				t.Fatalf("NeedsMembersOnDelete=%v want %v",
					tc.a.NeedsMembersOnDelete(), tc.needMem)
			}
		})
	}
}

func TestDeletePolicy(t *testing.T) {
	// (setup values, removed value) -> needsRecompute and expected state after
	// a full rebuild driven by Recompute semantics.
	cases := []struct {
		name        string
		a           Aggregator
		vals        []float64
		removed     float64
		needRecomp  bool
		survivors   []float64
		rebuiltWant float64
	}{
		{"count", NewCount(), []float64{0, 0, 0}, 0, false, []float64{0, 0}, 2},
		{"sum", NewSum(), []float64{1, 2, 3}, 2, false, []float64{1, 3}, 4},
		{"min-hit", NewMin(), []float64{1, 2, 3}, 1, true, []float64{2, 3}, 2},
		{"min-miss", NewMin(), []float64{1, 2, 3}, 3, false, nil, 1},
		{"max-hit", NewMax(), []float64{1, 2, 3}, 3, true, []float64{1, 2}, 2},
		{"max-miss", NewMax(), []float64{1, 2, 3}, 1, false, nil, 3},
		{"distinct", NewDistinctCount(), []float64{1, 1, 2}, 1, true, []float64{1, 2}, 2},
		{"distinct-gone", NewDistinctCount(), []float64{1, 2, 2}, 1, true, []float64{2, 2}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range tc.vals {
				tc.a.Insert(v)
			}
			got := tc.a.Delete(tc.removed)
			if got != tc.needRecomp {
				t.Fatalf("Delete needsRecompute=%v want %v", got, tc.needRecomp)
			}
			if tc.needRecomp {
				tc.a.Reset()
				for _, v := range tc.survivors {
					tc.a.Insert(v)
				}
			}
			if tc.a.Value() != tc.rebuiltWant {
				t.Fatalf("value=%v want %v", tc.a.Value(), tc.rebuiltWant)
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	want := []string{"count", "sum", "min", "max", "distinct_count"}
	got := Registry()
	if len(got) != len(want) {
		t.Fatalf("got %d aggregators want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name() != want[i] {
			t.Fatalf("[%d]=%q want %q", i, got[i].Name(), want[i])
		}
	}
}
