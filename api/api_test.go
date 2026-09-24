package api

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func recompute(ops []Op) map[string]map[string]int {
	tb, best := map[string]int64{}, map[string]Op{}
	for _, op := range ops {
		k := op.Outer + "|" + op.Inner
		if op.Kind == DelOuter {
			if op.TS > tb[op.Outer] {
				tb[op.Outer] = op.TS
			}
		} else if c := best[k]; op.TS > c.TS || op.TS == c.TS && op.Rep > c.Rep {
			best[k] = op
		}
	}
	out := map[string]map[string]int{}
	for _, e := range best {
		if e.TS > tb[e.Outer] {
			if out[e.Outer] == nil {
				out[e.Outer] = map[string]int{}
			}
			out[e.Outer][e.Inner] = e.Value
		}
	}
	return out
}
func apply(t *testing.T, r *API, ops ...Op) {
	for _, op := range ops {
		if err := r.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
}
func TestBatchRecomputeConsistency(t *testing.T) {
	cases := [][2][]Op{
		{{put("o", "k1", 10, 1, "A"), put("o", "k1", 20, 2, "A"), del("o", 3)},
			{put("o", "k2", 30, 3, "B"), put("o", "k3", 40, 2, "B")}},
		{{del("o", 2)}, {put("o", "k", 1, 3, "B")}},
		{{put("o", "k", 100, 5, "A")}, {put("o", "k", 999, 2, "B")}},
		{{put("o", "k", 1, 5, "A"), del("o", 4)}, {del("o", 6)}},
	}
	for _, c := range cases {
		x, y := New(), New()
		apply(t, x, c[0]...)
		apply(t, y, c[1]...)
		all := append(append([]Op{}, c[0]...), c[1]...)
		x.Merge(y)
		y.Merge(x)
		want := recompute(all)
		if got := x.View(); !reflect.DeepEqual(got, want) || !reflect.DeepEqual(y.View(), want) {
			t.Fatalf("incremental %v != batch %v", got, want)
		}
	}
}
func TestTombstonePropagation(t *testing.T) {
	cases := []struct {
		a, b []Op
		tomb int64
		view map[string]map[string]int
	}{
		{[]Op{put("o", "k1", 100, 1, "A"), del("o", 3)}, []Op{put("o", "k3", 300, 1, "B"), put("o", "k4", 400, 2, "B")}, 3, map[string]map[string]int{}},
		{[]Op{del("o", 3)}, []Op{del("o", 7)}, 7, map[string]map[string]int{}},
		{[]Op{del("o", 3)}, []Op{put("o", "eq", 1, 3, "C"), put("o", "gt", 2, 4, "D")}, 3, map[string]map[string]int{"o": {"gt": 2}}},
	}
	for _, tc := range cases {
		a, b := New(), New()
		apply(t, a, tc.a...)
		apply(t, b, tc.b...)
		a.Merge(b)
		b.Merge(a)
		got, _ := a.Tomb("o")
		if got != tc.tomb || !reflect.DeepEqual(a.View(), tc.view) || !reflect.DeepEqual(b.View(), tc.view) {
			t.Fatalf("tomb=%d a=%v b=%v", got, a.View(), b.View())
		}
	}
}
func TestLWWConvergence(t *testing.T) {
	cases := []struct {
		x, y Op
		w    int
	}{
		{put("o", "k", 100, 5, "A"), put("o", "k", 999, 2, "B"), 100},
		{put("o", "k", 1, 5, "A"), put("o", "k", 2, 5, "B"), 2},
		{put("o", "k", 999, 2, "B"), put("o", "k", 100, 5, "A"), 100},
	}
	for _, tc := range cases {
		x, y := New(), New()
		apply(t, x, tc.x)
		apply(t, y, tc.y)
		x.Merge(y)
		y.Merge(x)
		if x.View()["o"]["k"] != tc.w || y.View()["o"]["k"] != tc.w {
			t.Fatalf("x=%v y=%v want %d", x.View(), y.View(), tc.w)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	bad := []struct {
		op   Op
		want error
	}{
		{Op{Kind: Put, Inner: "x", Value: 1, TS: 1, Rep: "A"}, ErrEmptyOuterKey},
		{put("o", "", 1, 1, "A"), ErrEmptyInnerKey},
		{put("o", "x", 1, 0, "A"), ErrNonPositiveTS},
		{put("o", "x", 1, -3, "A"), ErrNonPositiveTS},
	}
	for _, tc := range bad {
		r := New()
		apply(t, r, put("o", "keep", 7, 1, "A"))
		before := r.View()
		if err := r.Apply(tc.op); err != tc.want || !reflect.DeepEqual(r.View(), before) {
			t.Fatalf("rejection wrong or left a trace: %v", err)
		}
		apply(t, r, put("o", "after", 9, 2, "A"))
	}
}
func TestConcurrentViewsIdentical(t *testing.T) {
	f := New()
	for j := range 200 {
		apply(t, f, put("o", fmt.Sprintf("i%03d", j), j, int64(j+1), "R"))
	}
	base := f.View()
	var wg sync.WaitGroup
	bad := int32(0)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if !reflect.DeepEqual(f.View(), base) || New().SelfCheck() != nil {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}()
	}
	wg.Wait()
	if bad != 0 {
		t.Fatal("concurrent views diverged or SelfCheck failed")
	}
}
