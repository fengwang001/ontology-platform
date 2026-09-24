package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/cntwin"
)

var failed bool

func ok(cond bool, msg string) {
	if !cond {
		failed = true
		fmt.Println("FAIL", msg)
		return
	}
	fmt.Println("OK", msg)
}

type kw struct {
	k string
	w int64
}

// model 按规则独立重算：返回各窗口接受元素之和与丢弃数。
func model(evs []api.Event, size, lateness int64) (map[kw]int64, int64) {
	acc, wm, cnt := map[kw]int64{}, map[string]int64{}, map[kw]int64{}
	seen := map[[2]interface{}]bool{} // 按 (Key,Pos) 去重，与 Val 无关
	var dropped int64
	for _, ev := range evs {
		if seen[[2]interface{}{ev.Key, ev.Pos}] {
			continue
		}
		seen[[2]interface{}{ev.Key, ev.Pos}] = true
		k := kw{ev.Key, cntwin.Window(ev.Pos, size)}
		w, has := wm[ev.Key]
		if !has {
			w = -1
		}
		if cnt[k] == size || (cntwin.Late(ev.Pos, w) && !cntwin.Acceptable(ev.Pos, w, lateness)) {
			dropped++
			continue
		}
		acc[k] += ev.Val
		cnt[k]++
		if ev.Pos > w {
			wm[ev.Key] = ev.Pos
		}
	}
	return acc, dropped
}

func main() {
	eng, _ := api.New(5, 2)
	poss := []int64{0, 1, 2, 3, 4, 5, 9, 7}
	vals := []int64{10, 20, 30, 40, 50, 60, 90, 70}
	attrib, noFire14, fire5, rest := true, true, false, true
	for i, p := range poss {
		out, err := eng.Feed([]api.Event{{Key: "k", Pos: p, Val: vals[i]}})
		attrib = attrib && err == nil && cntwin.Window(p, 5) == []int64{0, 0, 0, 0, 0, 1, 1, 1}[i]
		switch {
		case i < 4:
			noFire14 = noFire14 && len(out) == 0
		case i == 4:
			fire5 = len(out) == 1 && out[0].Win == 0 && out[0].Sum == 150
		default:
			rest = rest && len(out) == 0
		}
	}
	ok(attrib, "steps: window attribution floor(pos/5), step6 (5,60)->win1")
	ok(noFire14, "steps1-4: wm=0,1,2,3 win0 cnt=1..4, no trigger")
	ok(fire5, "step5: window [0,5) fires on (4,50), sum=150")
	ok(rest && eng.Dropped() == 0, "steps6-8: wm=5,9,9; step8 (7,70) late accepted (7>=9-2)")
	_, errNeg := eng.Feed([]api.Event{{Key: "k", Pos: -1, Val: 1}})
	ok(errors.Is(errNeg, api.ErrPos), "negative Pos rejected with ErrPos")

	seq := []api.Event{} // 确定性伪随机多 Key 序列，含迟到与重复
	for i := int64(0); i < 300; i++ {
		seq = append(seq, api.Event{Key: string(rune('a' + i%5)), Pos: (i*37 + 11) % 60, Val: i%17 - 8})
	}
	eng2, _ := api.New(5, 2)
	_, _ = eng2.Feed(seq)
	acc, dropped := model(seq, 5, 2)
	consistent, once := eng2.Dropped() == dropped, true
	seenFire := map[kw]bool{}
	for _, f := range eng2.Fired() {
		once = once && !seenFire[kw{f.Key, f.Win}]
		seenFire[kw{f.Key, f.Win}] = true
		consistent = consistent && acc[kw{f.Key, f.Win}] == f.Sum
	}
	ok(consistent, "Fired matches batch recompute")
	ok(once, "each (Key,window) fires at most once")

	e1, e2 := func() error { _, e := api.New(0, 1); return e }(), func() error { _, e := api.New(1, -1); return e }()
	_, e3 := eng2.Feed([]api.Event{{Key: "x", Pos: -2}})
	_, e4 := eng2.Feed([]api.Event{{Key: "", Pos: 0}})
	distinct := errors.Is(e1, api.ErrSize) && errors.Is(e2, api.ErrLateness) &&
		errors.Is(e3, api.ErrPos) && errors.Is(e4, api.ErrKey) &&
		e1 != e2 && e1 != e3 && e1 != e4 && e2 != e3 && e2 != e4 && e3 != e4
	before, beforeDrop := eng2.Fired(), eng2.Dropped()
	untouched := len(eng2.Fired()) == len(before) && eng2.Dropped() == beforeDrop
	ok(distinct && untouched, "4 distinct sentinel errors; rejected batch leaves state untouched")

	big, _ := api.New(5, 2) // m 个未触发窗口，只触发其中 1 个
	for m := 0; m < 10000; m++ {
		for p := int64(0); p < 4; p++ {
			_, _ = big.Feed([]api.Event{{Key: fmt.Sprint(m), Pos: p, Val: 1}})
		}
	}
	out, _ := big.Feed([]api.Event{{Key: "777", Pos: 4, Val: 1}})
	ok(len(out) == 1 && out[0].Key == "777" && len(big.Fired()) == 1, "m=10000 open windows: only targeted window fires (O(1) locate)")

	var wg sync.WaitGroup // 并发只读同一已喂满实例，结果逐字段相同
	same := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				fs, d := eng2.Fired(), eng2.Dropped()
				if d != dropped || len(fs) != len(before) {
					same = false
				}
			}
		}()
	}
	wg.Wait()
	ok(same && eng2.SelfCheck() == nil, "concurrent read-only identical; SelfCheck passed")

	if failed {
		os.Exit(1)
	}
}
