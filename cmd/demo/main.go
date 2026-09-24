package main

import (
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/wagg"
	"ontology/win"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	s := "OK"
	if !ok {
		s = "FAIL"
	}
	fmt.Printf("%s: %s\n", name, s)
}

var eightTS = []int64{2, 7, 13, 9, 18, 4, 10, 23}

func feed8() ([]api.Change, *api.API) {
	a, _ := api.New(10, 3, 5, 0)
	var log []api.Change
	for _, t := range eightTS {
		ch, _ := a.Feed([]api.Event{{Key: "K", TS: t}})
		log = append(log, ch...)
	}
	return append(log, a.Flush()...), a
}

func main() {
	neg := true
	for ts, w := range map[int64][2]int64{-25: {-30, -20}, -20: {-20, -10}, -1: {-10, 0}, 0: {0, 10}, 10: {10, 20}} {
		s, e := win.Bounds(ts, 10)
		neg = neg && s == w[0] && e == w[1]
	}
	check("negative-TS window bounds", neg)

	// 第三节八步：逐步比对变更日志（wagg 层），含第 3/6/7 步判定。
	agg, _ := wagg.New(10, 3, 5, 0)
	want := [][]wagg.Change{
		nil, nil,
		{{Key: "K", Start: 0, End: 10, Count: 2}}, // 第 3 步：TS=13 正常，触发 [0,10)
		{{Key: "K", Start: 0, End: 10, Count: 2, Retract: true}, {Key: "K", Start: 0, End: 10, Count: 3}},
		nil, nil, nil, // 第 6 步：wm==end+lateness 丢弃；第 7 步：正常无输出
		{{Key: "K", Start: 10, End: 20, Count: 3}},
	}
	ok := true
	for i, t := range eightTS {
		got, err := agg.Feed([]wagg.Event{{Key: "K", TS: t}})
		ok = ok && err == nil && reflect.DeepEqual(got, want[i])
	}
	check("eight-step changelog + steps 3/6/7 verdicts", ok && agg.Dropped() == 1)

	log, a := feed8()
	v := a.View()
	check("flush view == batch recompute", reflect.DeepEqual(v, map[api.ViewKey]int64{
		{Key: "K", Start: 0, End: 10}: 3, {Key: "K", Start: 10, End: 20}: 3, {Key: "K", Start: 20, End: 30}: 1}))
	cur, pre := map[api.ViewKey]int64{}, true
	for _, c := range log {
		k := api.ViewKey{Key: c.Key, Start: c.Start, End: c.End}
		if c.Retract {
			pre = pre && cur[k] == c.Count
			if cur[k] -= c.Count; cur[k] == 0 {
				delete(cur, k)
			}
		} else {
			cur[k] += c.Count
		}
	}
	check("changelog prefixes consistent", pre)

	_, e1 := api.New(0, 3, 5, 0)
	b, _ := api.New(10, 3, 5, 1)
	b.Feed([]api.Event{{Key: "a", TS: 1}})
	_, e2 := b.Feed([]api.Event{{Key: "b", TS: 2}})
	_, e3 := b.Feed([]api.Event{{Key: "", TS: 2}})
	check("three distinct sentinel errors", e1 == api.ErrParam && e2 == api.ErrMaxOpen && e3 == api.ErrEmptyKey &&
		e1 != e2 && e2 != e3 && e1 != e3)
	stable := len(b.View()) == 0 && b.Dropped() == 0 // 拒绝不留痕
	_, err := b.Feed([]api.Event{{Key: "a", TS: 3}})
	ch := b.Flush() // 拒绝后仍可用，且 a@1、a@3 都在
	check("rejected feed leaves state, still usable", stable && err == nil && len(ch) == 1 && ch[0].Count == 2)

	m, _ := api.New(10, 1<<40, 5, 0)
	evs := make([]api.Event, 10001)
	for i := 0; i < 10000; i++ {
		evs[i] = api.Event{Key: fmt.Sprintf("k%d", i), TS: int64(i) * 10}
	}
	evs[10000] = api.Event{Key: "last", TS: 99991}
	_, err = m.Feed(evs)
	check("large-m feed (checked counter pinned in wagg test)", err == nil && len(m.View()) == 0)

	start := make(chan struct{})
	var wg sync.WaitGroup
	cok := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				if !reflect.DeepEqual(a.View(), v) || a.Dropped() != 1 || a.SelfCheck() != nil {
					cok = false
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent read-only views identical", cok)

	if failed {
		fmt.Println("RESULT: FAIL")
	} else {
		fmt.Println("RESULT: OK")
	}
}
