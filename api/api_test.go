package api

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"ontology/agg"
	"ontology/delta"
)

func ref(m map[int64]int) (q Quad) { // from-scratch full recomputation over a live multiset
	for x, c := range m {
		q.Count += c
		q.Sum += x * int64(c)
		if !q.HasMin || x < q.Min {
			q.Min, q.HasMin = x, true
		}
		if !q.HasMax || x > q.Max {
			q.Max, q.HasMax = x, true
		}
	}
	return
}
func ins(k string, x int64) delta.Event { return delta.Event{Key: k, Val: x, Op: delta.Insert} }
func ret(k string, x int64) delta.Event { return delta.Event{Key: k, Val: x, Op: delta.Retract} }
func quad(n int, s, lo, hi int64) Quad  { return Quad{n, s, lo, hi, true, true} }
func TestSixStepTable(t *testing.T) {
	steps := []delta.Event{ins("g", 5), ins("g", 2), ins("g", 9), ret("g", 9), ins("g", 2), ret("g", 2)}
	want := []Quad{quad(1, 5, 5, 5), quad(2, 7, 2, 5), quad(3, 16, 2, 9), quad(2, 7, 2, 5), quad(3, 9, 2, 5), quad(2, 7, 2, 5)}
	v := New(4)
	for i, e := range steps {
		if err := v.Feed([]delta.Event{e}); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if got := v.Snapshot("g"); got != want[i] {
			t.Fatalf("step %d: %+v != %+v", i, got, want[i])
		}
	}
}
func TestStepwiseEqualsFullRecompute(t *testing.T) {
	for trial := 0; trial < 24; trial++ {
		rng := rand.New(rand.NewSource(int64(trial*7919 + 1)))
		v, live := New(8), map[string]map[int64]int{}
		for s := 0; s < 200+trial*25; s++ {
			k := []string{"a", "b", "c"}[rng.Intn(3)]
			m := live[k]
			if m == nil {
				m = map[int64]int{}
				live[k] = m
			}
			e := ins(k, int64(rng.Intn(21)-10))
			if len(m) > 0 && rng.Intn(2) == 0 {
				for x := range m {
					e = ret(k, x)
					break
				}
			}
			if err := v.Feed([]delta.Event{e}); err != nil {
				t.Fatalf("trial %d step %d: %v", trial, s, err)
			}
			if e.Op == delta.Insert {
				m[e.Val]++
			} else if m[e.Val]--; m[e.Val] == 0 {
				delete(m, e.Val)
			}
			if got := v.Snapshot(k); got != ref(m) {
				t.Fatalf("trial %d step %d key %s: %+v != %+v", trial, s, k, got, ref(m))
			}
		}
	}
}
func TestRetractIsInverseOfInsert(t *testing.T) {
	for _, x := range []int64{0, 7, -7, math.MaxInt64 / 2, math.MinInt64 / 2} {
		v := New(2)
		v.Feed([]delta.Event{ins("g", 3), ins("g", -3), ins("g", 10)})
		before := v.Snapshot("g")
		if err := v.Feed([]delta.Event{ins("g", x), ret("g", x)}); err != nil {
			t.Fatalf("x=%d: %v", x, err)
		}
		if got := v.Snapshot("g"); got != before {
			t.Fatalf("x=%d: %+v != %+v", x, got, before)
		}
	}
}
func TestEmptyGroupReportsAbsent(t *testing.T) {
	v := New(2)
	if q := v.Snapshot("g"); q.Count != 0 || q.Sum != 0 || q.HasMin || q.HasMax {
		t.Fatalf("fresh group %+v", q)
	}
	v.Feed([]delta.Event{ins("g", 5), ins("g", 2), ret("g", 5), ret("g", 2)})
	if q := v.Snapshot("g"); q.Count != 0 || q.Sum != 0 || q.HasMin || q.HasMax {
		t.Fatalf("drained group %+v: absent MIN/MAX must not be reported as 0", q)
	}
}
func TestRejectedEventsLeaveNoTrace(t *testing.T) {
	v := New(3)
	v.Feed([]delta.Event{ins("g", 1)})
	pre := v.Snapshot("g")
	cases := []struct {
		name string
		evs  []delta.Event
		want error
	}{
		{"retract unknown", []delta.Event{ret("g", 9)}, agg.ErrRetractUnknown},
		{"batch rolls back", []delta.Event{ins("g", 2), ret("g", 99)}, agg.ErrRetractUnknown},
		{"sum overflow", []delta.Event{ins("g", 2), ins("g", math.MaxInt64)}, agg.ErrSumOverflow},
		{"invalid op", []delta.Event{{Key: "g", Val: 1, Op: delta.Op(9)}}, delta.ErrInvalidOp},
	}
	for _, c := range cases {
		if err := v.Feed(c.evs); !errors.Is(err, c.want) || v.Snapshot("g") != pre {
			t.Fatalf("%s: err=%v trace=%+v != %+v", c.name, err, v.Snapshot("g"), pre)
		}
	}
	tm := New(1)
	tm.Feed([]delta.Event{ins("g", 1)})
	if err := tm.Feed([]delta.Event{ins("y", 1)}); !errors.Is(err, ErrTooManyGroups) || tm.Snapshot("y") != (Quad{}) {
		t.Fatalf("group cap leaked err=%v y=%+v", err, tm.Snapshot("y"))
	}
	if v.Feed([]delta.Event{ins("g", 3)}) != nil {
		t.Fatal("view unusable after rejection")
	}
}
func TestConcurrentSnapshotsConsistent(t *testing.T) {
	v := New(4)
	for i := 0; i < 250; i++ {
		_ = v.Feed([]delta.Event{ins("g", int64(i%17)-8)})
	}
	want := v.Snapshot("g")
	const n = 64
	barrier, ch := make(chan struct{}), make(chan Quad, n)
	for i := 0; i < n; i++ {
		go func() { <-barrier; ch <- v.Snapshot("g") }() // released together; no sleep
	}
	close(barrier)
	for i := 0; i < n; i++ {
		if q := <-ch; q != want {
			t.Fatalf("reader %d: %+v != %+v", i, q, want)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if err := New(8).SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if err := agg.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
