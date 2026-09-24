package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/alm"
	"ontology/api"
	"ontology/thr"
)

var failed bool

func report(name string, ok bool) {
	line := "OK  " + name
	if !ok {
		line = "FAIL " + name
		failed = true
	}
	fmt.Println(line)
}

// checkThr 用第三节八步序列直接核验 thr.Judge 的逐步输出。
func checkThr() {
	deltas := []int64{10, 2, -5, 2, -2, 4, -7, 3}
	wantKinds := []thr.Kind{thr.KindOn, thr.KindNone, thr.KindNone, thr.KindNone,
		thr.KindNone, thr.KindNone, thr.KindOff, thr.KindNone}
	wantOn := []bool{true, true, true, true, true, true, false, false}
	var v int64
	var on bool
	ok := true
	for i, d := range deltas {
		old := v
		v += d
		var k thr.Kind
		on, k = thr.Judge(old, v, on, 10, 3)
		if k != wantKinds[i] || on != wantOn[i] {
			ok = false
		}
	}
	report("thr judge eight-step", ok)
}

// checkAlm 用 Store 跑八步，核验事件序列（ON@1、OFF@7）与最终 (value=7, on=OFF)。
func checkAlm() {
	s, err := alm.NewStore(10, 3, 16)
	if err != nil {
		report("alm store eight-step", false)
		return
	}
	deltas := []int64{10, 2, -5, 2, -2, 4, -7, 3}
	var got []alm.Event
	for _, d := range deltas {
		evs, err := s.Add("k", d)
		if err != nil {
			report("alm store eight-step", false)
			return
		}
		got = append(got, evs...)
	}
	ok := len(got) == 2 &&
		got[0].Kind == thr.KindOn && got[0].Value == 10 && got[0].Seq == 1 &&
		got[1].Kind == thr.KindOff && got[1].Value == 4 && got[1].Seq == 2 &&
		s.View()["k"] == 7 && !s.Alarm()["k"]
	report("alm store eight-step", ok)
}

// checkAPI 跑 api.SelfCheck 的全部内置自检项。
func checkAPI() {
	a, err := api.New(10, 3, 16)
	if err != nil {
		report("selfcheck", false)
		return
	}
	for _, c := range a.SelfCheck() {
		report("selfcheck "+c.Name, c.OK)
	}
}

// checkConcurrent 64 个 goroutine 并发 Add 到互不相同的 Key，与串行参照一致。
func checkConcurrent() {
	const n = 64
	a, err := api.New(10, 3, n)
	if err != nil {
		report("concurrent-add", false)
		return
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i)
			for d := 1; d <= 4; d++ { // 每个 Key 累计 10，恰好触发 ON
				if _, err := a.Add(key, int64(d)); err != nil {
					panic(err)
				}
			}
		}(i)
	}
	wg.Wait()
	ok := len(a.Events()) == n
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%d", i)
		if a.View()[k] != 10 || !a.Alarm()[k] {
			ok = false
		}
	}
	report("concurrent-add", ok)
}

func main() {
	checkThr()
	checkAlm()
	checkAPI()
	checkConcurrent()
	if failed {
		os.Exit(1)
	}
}
