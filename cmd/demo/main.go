package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/export"
	"ontology/snap"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// snap：点时刻副本 + 严格 > 定位
	src := map[string]int64{"b": 2, "a": 1, "c": 3}
	s := snap.NewSnapshot(src)
	src["a"] = 99
	idx, _ := s.Locate("a")
	e := s.Entries(idx, 2)
	check("snap: copy isolated, strict > locate", s.Len() == 3 && idx == 1 &&
		len(e) == 2 && e[0].Key == "b" && e[1].Key == "c" && s.All()[0].Val == 1)

	// export：满块末块 done=true、续传不重复、完毕后 ErrFinished
	s6 := snap.NewSnapshot(map[string]int64{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6})
	ex := export.NewExporter(s6, 2)
	b1, c1, d1, _ := ex.Next("")
	b2, c2, d2, _ := ex.Resume(c1)
	b3, _, d3, _ := ex.Next(c2)
	_, _, _, errFin := ex.Next("f")
	check("export: chunks, done on full last block, resume no dup",
		len(b1) == 2 && len(b2) == 2 && len(b3) == 2 && b2[0].Key == "c" &&
			!d1 && !d2 && d3 && errors.Is(errFin, export.ErrFinished))

	// api：第三节五步序列（逐步块与位点）
	a, _ := api.New(2)
	for i, k := range []string{"a", "b", "c", "d", "e", "f"} {
		_ = a.Put(k, int64(i+1))
	}
	a.Snapshot()
	st1, cur1, dn1, _ := a.Next("")
	_ = a.Put("d", 99)
	st2, cur2, dn2, _ := a.Next("b")
	st3, cur3, dn3, _ := a.Next("d")
	_, _, _, err5 := a.Next("f")
	check(fmt.Sprintf("five-step: %v>%s %v>%s %v>%s done, 5th=ErrFinished", st1, cur1, st2, cur2, st3, cur3),
		fmt.Sprint(st1) == "[{a 1} {b 2}]" && cur1 == "b" && !dn1 &&
			fmt.Sprint(st2) == "[{c 3} {d 4}]" && cur2 == "d" && !dn2 &&
			fmt.Sprint(st3) == "[{e 5} {f 6}]" && cur3 == "f" && dn3 &&
			errors.Is(err5, api.ErrFinished))

	// 三块拼接 == 快照时刻全量
	full := append(append(append([]api.Entry{}, st1...), st2...), st3...)
	check("concat == snapshot-time full (d=4 not 99)",
		fmt.Sprint(full) == "[{a 1} {b 2} {c 3} {d 4} {e 5} {f 6}]")

	// 三类陷阱的具体错值
	keys := []string{"a", "b", "c", "d", "e", "f"}
	ge := keys[sort.SearchStrings(keys, "b")] // 甲：>= 定位把 b 再导一次
	liveD := map[string]int64{"d": 99}["d"]   // 乙：活视图把 d 导成 99
	wrongDone := len(st3) < 2                 // 丙：短块判 done，满块末块错成 false
	check("traps: >= re-exports b; live-view d=99; short-block done=false",
		ge == "b" && liveD == 99 && !wrongDone && dn3)

	// 四类可判定错误互不相同
	errs := []error{api.ErrBadChunk, api.ErrEmptyKey, api.ErrNoSnapshot, api.ErrFinished}
	distinct := true
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	_, errBad := api.New(0)
	fresh, _ := api.New(1)
	_, _, _, errNo := fresh.Next("")
	check("four sentinel errors distinct & triggerable",
		distinct && errors.Is(errBad, api.ErrBadChunk) && errors.Is(a.Put("", 1), api.ErrEmptyKey) &&
			errors.Is(a.Del(""), api.ErrEmptyKey) && errors.Is(errNo, api.ErrNoSnapshot) &&
			errors.Is(err5, api.ErrFinished))

	// 被拒后状态不变
	before := fmt.Sprint(a.View())
	_, _, _, _ = a.Next("f")
	check("rejected ops leave state unchanged", fmt.Sprint(a.View()) == before)

	// 大 n：中间位点正确定位（比较次数不随 n 增长由 export 白盒测试钉住）
	big := map[string]int64{}
	for i := 0; i < 10000; i++ {
		big[fmt.Sprintf("k%05d", i)] = int64(i)
	}
	bx := export.NewExporter(snap.NewSnapshot(big), 7)
	blk, _, _, _ := bx.Next("k05000")
	check("big-n: mid-cursor block starts at k05001 (sub-linear: export white-box test)",
		len(blk) == 7 && blk[0].Key == "k05001")

	// 并发：导出期间并发 Put，结果仍等于快照时刻全量
	c, _ := api.New(4)
	for i := 0; i < 50; i++ {
		_ = c.Put(fmt.Sprintf("k%03d", i), int64(i))
	}
	want := fmt.Sprint(c.View())
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				_ = c.Put(fmt.Sprintf("w%d-%03d", w, i), int64(i))
			}
		}(w)
	}
	c.Snapshot()
	close(start)
	var gotC []api.Entry
	cur := ""
	for {
		blk, nc, done, err := c.Next(cur)
		if err != nil {
			break
		}
		gotC = append(gotC, blk...)
		cur = nc
		if done {
			break
		}
	}
	wg.Wait()
	check("concurrent export == snapshot-time full", fmt.Sprint(gotC) == want)

	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
