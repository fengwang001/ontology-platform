package main

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"

	"ontology/agg"
	"ontology/api"
	"ontology/spill"
)

func ok(name string, good bool) {
	if good {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func eightEvents() []api.Event {
	return []api.Event{
		{Key: "a", Val: 5}, {Key: "b", Val: 3}, {Key: "c", Val: 7}, {Key: "d", Val: 2},
		{Key: "b", Val: 4}, {Key: "a", Val: 1}, {Key: "c", Val: 2}, {Key: "d", Val: 5},
	}
}

func main() {
	// spill 包判定：溢写最久未访问者→回载部分和+新值并删除溢写条目。
	s := spill.New()
	for _, e := range []struct {
		k string
		v int64
	}{{"a", 5}, {"b", 3}, {"c", 7}, {"d", 2}} {
		s.PutResident(e.k, e.v)
	}
	k, _ := s.Evict()
	st, v := s.Lookup("a")
	merged := s.LoadIn("a", 1)
	r, sp := s.Snapshot()
	_, aInSpill := sp["a"]
	ok("spill lru-evict+reload", k == "a" && st == spill.Spilled && v == 5 &&
		merged == 6 && r["a"] == 6 && !aInSpill)

	// agg 包判定：八步每步之后常驻区/溢写区全貌 + 第4/6/7/8步溢写者 + 常驻数≤3。
	evs := eightEvents()
	wantR := []map[string]int64{
		{"a": 5},
		{"a": 5, "b": 3},
		{"a": 5, "b": 3, "c": 7},
		{"b": 3, "c": 7, "d": 2},
		{"b": 7, "c": 7, "d": 2},
		{"a": 6, "b": 7, "d": 2},
		{"a": 6, "b": 7, "c": 9},
		{"a": 6, "c": 9, "d": 7},
	}
	wantS := []map[string]int64{
		{}, {}, {},
		{"a": 5}, {"a": 5}, {"c": 7}, {"d": 2}, {"b": 7},
	}
	trace := agg.StepTrace(3, toAgg(evs))
	wantKind := []string{"insert", "insert", "insert", "insert",
		"update", "reload", "reload", "reload"}
	wantEv := []string{"", "", "", "a", "", "c", "d", "b"}
	good := len(trace) == 8
	for i := range trace {
		good = good && trace[i].Kind == wantKind[i] && trace[i].EvictedKey == wantEv[i] &&
			reflect.DeepEqual(trace[i].Resident, wantR[i]) &&
			reflect.DeepEqual(trace[i].Spilled, wantS[i]) &&
			len(trace[i].Resident) <= 3
	}
	ok("agg 8-step states+evict@4,6,7,8", good)

	// api 包判定。
	a, _ := api.New(3)
	_ = a.Feed(evs)
	batch := map[string]int64{"a": 6, "b": 7, "c": 9, "d": 7}
	ok("api reload merge + view==batch", a.View()["a"] == 6 &&
		reflect.DeepEqual(a.View(), batch) && a.Spills() == 4 && a.Loads() == 3)

	// 三类可判定哨兵错误互不相同。
	_, errLimit := api.New(0)
	errKey := a.Feed([]api.Event{{Key: "", Val: 1}})
	errOver := a.Feed([]api.Event{{Key: "z", Val: math.MaxInt64}, {Key: "z", Val: 1}})
	ok("api 3 distinct sentinel errors", errors.Is(errLimit, api.ErrInvalidLimit) &&
		errors.Is(errKey, api.ErrEmptyKey) && errors.Is(errOver, api.ErrOverflow) &&
		api.ErrInvalidLimit != api.ErrEmptyKey && api.ErrEmptyKey != api.ErrOverflow)

	// 被拒整批不留痕：值、溢写/回载计数全部不变，之后仍可正常 Feed。
	snap, spN, ldN := a.View(), a.Spills(), a.Loads()
	ok("api rejected batch atomic", reflect.DeepEqual(a.View(), snap) &&
		a.Spills() == spN && a.Loads() == ldN && a.Feed([]api.Event{{Key: "q", Val: 1}}) == nil)

	// 大 m 下溢写检查个数不随 m 线性增长（包内实验，只回布尔）。
	ok("spill probe O(1) at m=100..10000", spill.VerifyProbeConstant())

	// 并发只读：N 个 goroutine 的 View 逐字段相同，无 sleep。
	full, _ := api.New(4)
	_ = full.Feed(evs)
	var wg sync.WaitGroup
	views := make([]map[string]int64, 16)
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); views[i] = full.View() }(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < len(views); i++ {
		same = same && reflect.DeepEqual(views[0], views[i])
	}
	ok("api concurrent readers identical", same && full.SelfCheck() == nil)
}

func toAgg(evs []api.Event) []agg.Event {
	out := make([]agg.Event, len(evs))
	for i, e := range evs {
		out[i] = agg.Event{Key: e.Key, Val: e.Val}
	}
	return out
}
