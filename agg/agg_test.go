package agg

import (
	"math"
	"ontology/delta"
	"testing"
)

// TestApplyVisitsLogM proves no per-event full rescan: after building a group
// of m live values, one Apply visits O(log m) stored values under a bound
// C*log2(m)+K with m-independent constants. A linear scan visits m values and
// blows the bound by hundreds at m=10000.
func TestApplyVisitsLogM(t *testing.T) {
	const mul = int64(7919) // spread keys; priorities hash64(key) are iid-like
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		g := New()
		for i := 0; i < m; i++ {
			if err := g.Apply(delta.Event{Val: int64(i) * mul, Op: delta.Insert}); err != nil {
				t.Fatalf("m=%d seed %d: %v", m, i, err)
			}
		}
		bound := 4.0*math.Log2(float64(m)) + 8.0
		probes := []delta.Event{
			{Val: int64(m)*mul + 1, Op: delta.Insert},  // fresh leaf
			{Val: int64(m)*mul + 1, Op: delta.Retract}, // that leaf
			{Val: int64(m/2) * mul, Op: delta.Retract}, // interior node
		}
		for i, ev := range probes {
			if err := g.Apply(ev); err != nil {
				t.Fatalf("m=%d probe %d: %v", m, i, err)
			}
			if float64(g.touched) > bound {
				t.Errorf("m=%d probe %d visited %d > bound %.1f", m, i, g.touched, bound)
			}
		}
	}
}

// TestSentinelRejectionsLeaveNoTrace checks the two agg sentinels are
// distinguishable and a rejected Apply changes no aggregate state.
func TestSentinelRejectionsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		setup []delta.Event
		ev    delta.Event
		want  error
	}{
		{
			"retract never inserted",
			nil,
			delta.Event{Val: 1, Op: delta.Retract},
			ErrRetractUnknown,
		},
		{
			"insert overflows max",
			[]delta.Event{{Val: math.MaxInt64, Op: delta.Insert}},
			delta.Event{Val: 1, Op: delta.Insert},
			ErrSumOverflow,
		},
		{
			"insert overflows min",
			[]delta.Event{{Val: math.MinInt64, Op: delta.Insert}},
			delta.Event{Val: -1, Op: delta.Insert},
			ErrSumOverflow,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			var n0 int
			var s0 int64
			for _, e := range tc.setup {
				if err := g.Apply(e); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			n0, s0 = g.Count(), g.Sum()
			err := g.Apply(tc.ev)
			if err != tc.want {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if g.Count() != n0 || g.Sum() != s0 {
				t.Fatalf("rejection left trace: n %d->%d sum %d->%d", n0, g.Count(), s0, g.Sum())
			}
		})
	}
}
