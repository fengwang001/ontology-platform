package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func ev(k string, ts int64, s byte) api.Event { return api.Event{Key: k, TS: ts, Side: s} }
func jn(k string, w, l, r int64) api.Join {
	return api.Join{Key: k, WindowStart: w, LeftTS: l, RightTS: r}
}
func eqMS(a, b []api.Join) bool { // multiset equality
	c := map[api.Join]int{}
	for _, j := range a {
		c[j]++
	}
	for _, j := range b {
		c[j]--
	}
	for _, v := range c {
		if v != 0 {
			return false
		}
	}
	return true
}
func TestFlushMatchesBatch(t *testing.T) {
	verify := func(w, d int64, evs []api.Event, tag string) {
		t.Helper()
		j, _ := api.New(w, d)
		j.Feed(evs)
		j.Flush()
		if got, want := j.Joins(), api.Batch(w, d, evs); !eqMS(got, want) || j.Retained() != 0 {
			t.Fatalf("%s: got %d want %d joins, retained %d", tag, len(got), len(want), j.Retained())
		}
	}
	verify(10, 3, []api.Event{ev("k", 5, 'L'), ev("k", 6, 'R'), ev("k", 12, 'L'), ev("k", 8, 'R'), ev("k", 2, 'L'), ev("k", 15, 'R')}, "six")
	verify(10, 0, []api.Event{ev("a", -5, 'L'), ev("a", 5, 'R'), ev("b", -6, 'R'), ev("a", -4, 'R'), ev("b", -7, 'L')}, "neg")
	verify(5, 1, []api.Event{ev("x", 100, 'L'), ev("x", 50, 'R'), ev("x", 95, 'R'), ev("x", 101, 'R'), ev("y", 101, 'L')}, "late")
	r := rand.New(rand.NewSource(1)) // random arrival orders, scales, negatives
	for n := 0; n < 20; n++ {
		var evs []api.Event
		for i := 0; i < 50; i++ {
			evs = append(evs, ev(fmt.Sprint(r.Intn(4)), int64(r.Intn(200)-50), "LR"[r.Intn(2)]))
		}
		verify(10, int64(n%5), evs, fmt.Sprint("rand", n))
	}
}
func TestNoDuplicateJoins(t *testing.T) {
	j, _ := api.New(100, 0)
	var evs []api.Event
	for i := 0; i < 30; i++ { // distinct TS per event: tuples are unique
		evs = append(evs, ev("k", int64(i), 'L'), ev("k", int64(i+30), 'R'))
	}
	j.Feed(evs)
	set := map[api.Join]bool{}
	for _, jn := range j.Joins() {
		set[jn] = true
	}
	if len(j.Joins()) != 900 || len(set) != 900 { // 30 L x 30 R, each pair once
		t.Fatalf("joins=%d distinct=%d, want 900", len(j.Joins()), len(set))
	}
}
func TestWatermarkMonotonic(t *testing.T) {
	j, _ := api.New(10, 0)
	j.Feed([]api.Event{ev("k", 100, 'L'), ev("k", 50, 'R'), ev("k", 95, 'R')})
	if j.Dropped() != 2 || j.Retained() != 1 || len(j.Joins()) != 0 {
		t.Fatalf("wm regressed: dropped=%d retained=%d joins=%v", j.Dropped(), j.Retained(), j.Joins())
	}
}
func TestRejectedFeedLeavesNoTrace(t *testing.T) {
	if len(map[error]bool{api.ErrBadWindow: true, api.ErrBadDelay: true, api.ErrEmptyKey: true, api.ErrBadSide: true}) != 4 {
		t.Fatal("sentinel errors not distinct")
	}
	for i, c := range []struct {
		w, d int64
		want error
	}{{0, 1, api.ErrBadWindow}, {1, -1, api.ErrBadDelay}} {
		if _, err := api.New(c.w, c.d); !errors.Is(err, c.want) {
			t.Fatalf("New case %d: %v", i, err)
		}
	}
	j, _ := api.New(10, 3)
	j.Feed([]api.Event{ev("k", 5, 'L'), ev("k", 6, 'R')})
	js, dr, re := j.Joins(), j.Dropped(), j.Retained()
	bad := [][]api.Event{{ev("", 7, 'L')}, {ev("k", 7, 'X')}, {ev("k", 8, 'R'), ev("", 9, 'L')}} // valid prefix must not apply
	want := []error{api.ErrEmptyKey, api.ErrBadSide, api.ErrEmptyKey}
	for i := range bad {
		if _, err := j.Feed(bad[i]); !errors.Is(err, want[i]) {
			t.Fatalf("bad[%d]: err=%v, want %v", i, err, want[i])
		}
		if !slices.Equal(j.Joins(), js) || j.Dropped() != dr || j.Retained() != re {
			t.Fatalf("bad[%d] left a trace", i)
		}
	}
	if _, err := j.Feed([]api.Event{ev("k", 8, 'R')}); err != nil || len(j.Joins()) != 2 {
		t.Fatal("joiner unusable after rejection")
	}
}
func TestSixStepScenario(t *testing.T) {
	j, _ := api.New(10, 3)
	steps := []api.Event{ev("k", 5, 'L'), ev("k", 6, 'R'), ev("k", 12, 'L'), ev("k", 8, 'R'), ev("k", 2, 'L'), ev("k", 15, 'R')}
	want := [][]api.Join{nil, {jn("k", 0, 5, 6)}, nil, {jn("k", 0, 5, 8)}, {jn("k", 0, 2, 6), jn("k", 0, 2, 8)}, {jn("k", 10, 12, 15)}}
	got := make([][]api.Join, 6)
	for i, e := range steps {
		got[i], _ = j.Feed([]api.Event{e})
	}
	if !slices.EqualFunc(got, want, slices.Equal) || len(j.Joins()) != 5 || j.Retained() != 2 {
		t.Fatalf("got %v (total %d, retained %d)", got, len(j.Joins()), j.Retained())
	}
	j2, _ := api.New(10, 0) // negative TS: floor-divided windows
	j2.Feed([]api.Event{ev("k", -5, 'L'), ev("k", 5, 'R')})
	j3, _ := api.New(10, 0)
	j3.Feed([]api.Event{ev("k", -5, 'L'), ev("k", -1, 'R')})
	if got3 := j3.Joins(); len(j2.Joins()) != 0 || len(got3) != 1 || got3[0] != jn("k", -10, -5, -1) {
		t.Fatalf("negative window: %v %v", j2.Joins(), got3)
	}
}
func TestConcurrentCrossProduct(t *testing.T) {
	for _, ab := range [][2]int{{1, 1}, {5, 7}, {20, 20}} {
		a, b := ab[0], ab[1]
		j, _ := api.New(1000, 1<<40)
		var wg sync.WaitGroup
		for i := 0; i < a+b; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				j.Feed([]api.Event{ev("k", int64(i), "LR"[min(i/a, 1)])})
				j.Joins()
				j.Retained()
				j.Dropped()
				j.SelfCheck()
			}(i)
		}
		wg.Wait()
		set := map[api.Join]bool{}
		for _, jn := range j.Joins() {
			set[jn] = true
		}
		if len(j.Joins()) != a*b || len(set) != a*b {
			t.Fatalf("a=%d b=%d: joins=%d distinct=%d, want %d", a, b, len(j.Joins()), len(set), a*b)
		}
	}
}
