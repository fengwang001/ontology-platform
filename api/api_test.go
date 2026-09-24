package api

import (
	"errors"
	"math/rand"
	"ontology/lagview"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// genOps: n pseudo-random inserts (LCG) then delete+reinsert churn; [:n] is inserts-only.
func genOps(seed uint64, n int) []Op {
	var ops []Op
	r := seed
	for i := 1; i <= n; i++ {
		r = r*6364136223846793005 + 1442695040888963407
		ops = append(ops, Op{ID: int64(i), Part: string(rune('a' + r>>33%3)), Sort: int64(r >> 43 % 50), Val: int64(r >> 53 % 10)})
	}
	for i := 2; i <= n; i += 3 {
		ops = append(ops, Op{Del: true, ID: int64(i)}, Op{ID: int64(i), Part: "z", Sort: int64(i % 7), Val: int64(i % 5)})
	}
	return ops
}
func TestEightStepChangelog(t *testing.T) {
	p := func(v int64) *int64 { return &v }
	ops := []Op{{ID: 1, Part: "p", Sort: 10, Val: 5}, {ID: 2, Part: "p", Sort: 30, Val: 8},
		{ID: 3, Part: "p", Sort: 20, Val: 7}, {ID: 4, Part: "q", Sort: 15, Val: 9},
		{ID: 5, Part: "p", Sort: 20, Val: 6}, {Del: true, ID: 3}, {ID: 6, Part: "p", Sort: 5, Val: 5}, {Del: true, ID: 1}}
	want := [][]Change{{{ID: 1}}, {{ID: 2, Lag: p(5)}},
		{{ID: 3, Lag: p(5)}, {Del: true, ID: 2, Lag: p(5)}, {ID: 2, Lag: p(7)}}, {{ID: 4}},
		{{ID: 5, Lag: p(7)}, {Del: true, ID: 2, Lag: p(7)}, {ID: 2, Lag: p(6)}},
		{{Del: true, ID: 3, Lag: p(5)}, {Del: true, ID: 5, Lag: p(7)}, {ID: 5, Lag: p(5)}},
		{{ID: 6}, {Del: true, ID: 1}, {ID: 1, Lag: p(5)}}, {{Del: true, ID: 1, Lag: p(5)}}}
	eq := func(a, b Change) bool { return a.Del == b.Del && a.ID == b.ID && lagview.EqLag(a.Lag, b.Lag) }
	x := New(100)
	for i, op := range ops {
		cs, err := x.Apply([]Op{op})
		if err != nil || !slices.EqualFunc(cs, want[i], eq) {
			t.Fatalf("step %d: got %v err %v, want %v", i+1, cs, err, want[i])
		}
	}
}

func TestViewMatchesBatchEveryStep(t *testing.T) {
	for seed := uint64(1); seed <= 5; seed++ {
		x := New(1 << 20)
		for i, op := range genOps(seed, 120) {
			if _, err := x.Apply([]Op{op}); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, i, err)
			}
			if !eqView(x.View(), x.lv.Batch()) {
				t.Fatalf("seed %d step %d: view != batch recompute", seed, i)
			}
		}
	}
}

func TestChangelogPrefixConsistency(t *testing.T) {
	x, down := New(1<<20), map[int64]*int64{}
	for i, op := range genOps(6, 200) {
		cs, _ := x.Apply([]Op{op})
		for _, c := range cs {
			if !lagview.ApplyChange(down, c) {
				t.Fatalf("step %d: inconsistent changelog entry %+v", i, c)
			}
		}
		if !eqView(x.View(), down) {
			t.Fatalf("step %d: downstream view diverged", i)
		}
	}
}

func TestInsertOrderIndependence(t *testing.T) {
	base := genOps(9, 80)[:80] // inserts only
	for trial := 0; trial < 5; trial++ {
		a, b := New(1<<20), New(1<<20)
		_, _ = a.Apply(base)
		for _, i := range rand.New(rand.NewSource(int64(trial))).Perm(len(base)) {
			_, _ = b.Apply([]Op{base[i]})
		}
		if !eqView(a.View(), b.View()) {
			t.Fatalf("trial %d: order-dependent view", trial)
		}
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		max  int
		bad  []Op
		want error
	}{
		{10, []Op{{ID: 1, Part: "q"}}, ErrDupID},                     // duplicate id
		{10, []Op{{ID: 2, Part: "p"}, {ID: 2, Part: "q"}}, ErrDupID}, // duplicate within the batch
		{10, []Op{{Del: true, ID: 7}}, ErrNoID},                      // missing id
		{10, []Op{{Del: true, ID: 1}, {Del: true, ID: 1}}, ErrNoID},  // deleted twice in one batch
		{10, []Op{{ID: 5}}, ErrBadPart},                              // empty part
		{1, []Op{{ID: 2, Part: "p"}}, ErrTooMany},                    // over maxRows
		{10, []Op{{ID: 2, Part: "p"}, {Del: true, ID: 9}}, ErrNoID},  // atomic: good op then bad op
	}
	for i, tc := range cases {
		x := New(tc.max)
		_, _ = x.Apply([]Op{{ID: 1, Part: "p"}})
		before := x.View()
		cs, err := x.Apply(tc.bad)
		if cs != nil {
			t.Fatalf("case %d: rejected batch emitted %v", i, cs)
		}
		for _, s := range []error{ErrDupID, ErrNoID, ErrBadPart, ErrTooMany} {
			if errors.Is(err, s) != (s == tc.want) {
				t.Fatalf("case %d: err=%v, want exactly %v", i, err, tc.want)
			}
		}
		if !eqView(before, x.View()) {
			t.Fatalf("case %d: rejected batch changed the view", i)
		}
		if _, err := x.Apply([]Op{{Del: true, ID: 1}}); err != nil {
			t.Fatalf("case %d: unusable after rejection: %v", i, err)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	x, y := New(500), New(1<<20)
	_, _ = x.Apply(genOps(3, 500)[:500])
	gold := x.View()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 16; g++ {
		wg.Go(func() {
			for i := 0; i < 20; i++ {
				if !eqView(x.View(), gold) || x.SelfCheck() != nil {
					bad.Store(true)
				}
				_ = y.View() // races with the writer below, for -race
			}
		})
	}
	wg.Go(func() {
		for _, op := range genOps(11, 300) {
			_, _ = y.Apply([]Op{op})
		}
	})
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent readers got different views")
	}
}
