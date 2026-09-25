// demo 逐条打印 OK/FAIL，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"

	"ontology/api"
	"ontology/wagg"
	"ontology/win"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK  " + name)
}

// trace8 复算第三节八步：size=8, hop=4, delay=2，逐步核对 wm、落入窗口与触发输出。
func trace8() bool {
	a, err := wagg.New(8, 4, 2)
	if err != nil {
		return false
	}
	wantWin := [][2]int64{{0, 4}, {4, 8}, {8, 12}, {8, 12}, {12, 16}, {12, 16}, {16, 20}, {16, 20}}
	wantWM := []int64{4, 8, 10, 12, 14, 16, 18, 20}
	wantFire := map[int]string{2: "{0 8 1}", 4: "{4 12 2}", 6: "{8 16 3}", 8: "{12 20 4}"}
	for i, ts := range []int64{6, 10, 12, 14, 16, 18, 20, 22} {
		ws := win.Windows(ts, 8, 4)
		if len(ws) != 2 || ws[0].Start != wantWin[i][0] || ws[1].Start != wantWin[i][1] || ts-2 != wantWM[i] {
			return false
		}
		cs, err := a.Feed([]wagg.Event{{Key: "k", TS: ts}})
		if err != nil {
			return false
		}
		got := ""
		if len(cs) == 1 {
			got = fmt.Sprintf("{%d %d %d}", cs[0].Start, cs[0].End, cs[0].Count)
		}
		if want, ok := wantFire[i+1]; ok != (got != "") || (ok && got != want) {
			return false
		}
	}
	return true
}

func fedEngine() (*api.Engine, []api.Change, []api.Event) {
	seq := []api.Event{{Key: "b", TS: -5}, {Key: "b", TS: -1}}
	for _, ts := range []int64{6, 10, 12, 14, 16, 18, 20, 22} {
		seq = append(seq, api.Event{Key: "a", TS: ts})
	}
	eng, err := api.New(8, 4, 2)
	if err != nil {
		return nil, nil, nil
	}
	log, err := eng.Feed(seq)
	if err != nil {
		return nil, nil, nil
	}
	log = append(log, eng.Flush()...)
	return eng, log, seq
}

func batchView(seq []api.Event) map[api.Key]int64 {
	v := map[api.Key]int64{}
	for _, e := range seq {
		for _, w := range win.Windows(e.TS, 8, 4) {
			v[api.Key{Key: e.Key, Start: w.Start, End: w.End}]++
		}
	}
	return v
}

func main() {
	w12, w16 := win.Windows(12, 8, 4), win.Windows(16, 8, 4)
	check("TS=12/16 窗口归属", fmt.Sprint(w12) == "[{8 16} {12 20}]" &&
		fmt.Sprint(w16) == "[{12 20} {16 24}]")
	check("八步轨迹 wm/落入/触发", trace8())
	eng, log, seq := fedEngine()
	check("Flush 后视图==批量重算", eng != nil && maps.Equal(eng.View(), batchView(seq)))

	once, ordered := true, true
	seen := map[api.Key]bool{}
	prevEnd := int64(-1 << 62)
	for _, c := range log {
		k := api.Key{Key: c.Key, Start: c.Start, End: c.End}
		once = once && !seen[k] && c.Count == batchView(seq)[k]
		ordered = ordered && c.End >= prevEnd
		seen[k], prevEnd = true, c.End
	}
	check("变更日志每窗口恰好一条且 end 升序", once && ordered && len(log) == len(batchView(seq)))

	e1, e2 := func() error { _, err := api.New(0, 4, 0); return err }(),
		func() error { _, err := api.New(8, 4, -1); return err }()
	bad, e3 := api.New(8, 4, 0)
	if e3 == nil {
		_, e3 = bad.Feed([]api.Event{{Key: "a", TS: 5}, {Key: "a", TS: 3}})
	}
	check("三类哨兵错误互不相同", errors.Is(e1, api.ErrParam) && errors.Is(e2, api.ErrDelay) &&
		errors.Is(e3, api.ErrOrder) && e1 != e2 && e2 != e3 && e1 != e3)

	v1 := eng.View()
	_, errA := eng.Feed([]api.Event{{Key: "a", TS: 30}, {Key: "a", TS: 7}}) // 批内乱序
	v2 := eng.View()
	_, errB := eng.Feed([]api.Event{{Key: "a", TS: 30}}) // 被拒后仍可用
	check("被拒整批不留痕且可继续用", errors.Is(errA, api.ErrOrder) && maps.Equal(v1, v2) && errB == nil)

	big, _ := wagg.New(8, 4, 1<<60)
	evs := make([]wagg.Event, 10000)
	for i := range evs {
		evs[i] = wagg.Event{Key: fmt.Sprintf("k%d", i)}
	}
	cs1, err1 := big.Feed(evs)
	cs2, err2 := big.Feed([]wagg.Event{{Key: "z", TS: 4}}) // 只推进水位线，不跨任何 end
	check("大 m 水位线推进 O(1)（wagg 测试钉死）", err1 == nil && err2 == nil && len(cs1) == 0 && len(cs2) == 0)

	want := eng.View()
	var wg sync.WaitGroup
	race := make(chan bool, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				race <- maps.Equal(eng.View(), want) && eng.SelfCheck() == nil
			}
		}()
	}
	wg.Wait()
	close(race)
	allSame := true
	for ok := range race {
		allSame = allSame && ok
	}
	check("并发只读视图逐字段相同", allSame)
	check("SelfCheck", eng.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
