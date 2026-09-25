package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/compact"
	"ontology/rec"
)

var fails int

func report(tag string, okv bool, detail string) {
	if !okv {
		fails++
		fmt.Printf("FAIL %s %s\n", tag, detail)
		return
	}
	fmt.Printf("OK %s %s\n", tag, detail)
}

var ten = []rec.Rec{
	{Key: "a", Value: 1, TS: 1}, {Key: "b", Value: 10, TS: 2},
	{Key: "a", Value: 2, TS: 4}, {Key: "c", Value: 5, TS: 3},
	{Key: "b", Value: 0, TS: 5, Del: true}, {Key: "a", Value: 0, TS: 6, Del: true},
	{Key: "c", Value: 0, TS: 7, Del: true}, {Key: "a", Value: 3, TS: 8},
	{Key: "d", Value: 7, TS: 6}, {Key: "d", Value: 9, TS: 2},
}

func enc(r rec.Rec) string {
	d := "P"
	if r.Del {
		d = "D"
	}
	return fmt.Sprintf("%s(%d,%d,%s)", r.Key, r.Value, r.TS, d)
}

func find(rs []rec.Rec, k string) (rec.Rec, bool) {
	for _, r := range rs {
		if r.Key == k {
			return r, true
		}
	}
	return rec.Rec{}, false
}

func main() {
	const wantCand = "a(1,1,P)b(10,2,P)a(2,4,P)c(5,3,P)b(0,5,D)a(0,6,D)c(0,7,D)a(3,8,P)d(7,6,P)d(7,6,P)"
	// rec：墓碑识别 + 保留期边界 TS+R<=hi（含等号）。
	bT, cT, put := rec.Rec{TS: 5, Del: true}, rec.Rec{TS: 7, Del: true}, rec.Rec{Key: "a", Value: 3, TS: 8}
	report("rec.Drop", rec.IsTomb(bT) && rec.IsTomb(cT) && !rec.IsTomb(put) &&
		rec.Drop(bT, 10, 5) && !rec.Drop(cT, 10, 5) && !rec.Drop(put, 10, 5),
		"b ts5 dropped(10<=10); c ts7 kept(12>10); put kept")
	// 十条逐步存活候选；窗口 [0,9) 使两墓碑都不被保留期丢弃，专看折叠。
	cc := compact.New(5)
	var cand, flags strings.Builder
	prev := map[string]rec.Rec{}
	for _, r := range ten {
		cc.Add([]rec.Rec{r})
		w, _ := cc.Compact(0, 9)
		cur, _ := find(w, r.Key)
		cand.WriteString(enc(cur))
		if p, ok := prev[r.Key]; !ok || p.TS != cur.TS {
			flags.WriteByte('N')
		} else {
			flags.WriteByte('F')
		}
		prev[r.Key] = cur
	}
	report("stepwise", cand.String() == wantCand && flags.String() == "NNNNNNNNNF", cand.String())
	// 最终 [0,10) 压缩结果（此处才施加保留期）。
	got, _ := cc.Compact(0, 10)
	var res strings.Builder
	for i, r := range got {
		if i > 0 {
			res.WriteByte(' ')
		}
		fmt.Fprintf(&res, "%s%d/%d", r.Key, r.Value, r.TS)
		if r.Del {
			res.WriteByte('T')
		}
	}
	report("compact[0,10)", res.String() == "a3/8 c0/7T d7/6", res.String())
	// 甲：错把“最后到达”当最新 -> d 取 (9,2)，Value 错成 9（正解 7）。
	last := map[string]rec.Rec{}
	for _, r := range ten {
		last[r.Key] = r
	}
	d, _ := find(got, "d")
	report("甲 last-arrival", last["d"].Value == 9 && d.Value == 7, fmt.Sprintf("bug d.Value=%d correct=7", last["d"].Value))
	// 乙：严格 < 会保留 b{0,5,T}；丙：只删墓碑会复活 b{10,2,P}；正解 b 缺席。
	_, bKept := find(got, "b")
	report("乙丙 b tombstone", !bKept, "strict< keeps b{0,5,T}; resurrect emits b{10,2,P}; correct absent")
	// 朴素一致 + 无重复 Key + 不复活。
	naive := compact.Naive(ten, 0, 10, 5)
	dup, noDup := map[string]bool{}, true
	for _, r := range got {
		if dup[r.Key] {
			noDup = false
		}
		dup[r.Key] = true
	}
	report("invariants", reflect.DeepEqual(got, naive) && noDup && !bKept, "naive-equal, no dup key, b not resurrected")
	report("selfCheck", cc.SelfCheck() == nil, "per-key fold cmps bounded m=100..10000")
	// 四类哨兵互不相同；被拒后已 Feed 状态不变且仍可正常使用。
	a, _ := api.New(5)
	if err := a.Feed(ten); err != nil {
		fmt.Println("FAIL feed", err)
		os.Exit(1)
	}
	base := a.View()
	_, eR := api.New(-1)
	eK := a.Feed([]rec.Rec{{Key: "", TS: 1}})
	eT := a.Feed([]rec.Rec{{Key: "z", TS: -1}})
	_, eW := a.Compact(5, 2)
	distinct := !errors.Is(eR, eK) && !errors.Is(eK, eT) && !errors.Is(eK, eW) && !errors.Is(eT, eW)
	ag, _ := a.Compact(0, 10)
	errOK := errors.Is(eR, api.ErrBadRetention) && errors.Is(eK, api.ErrEmptyKey) &&
		errors.Is(eT, api.ErrNegativeTS) && errors.Is(eW, compact.ErrBadWindow) && distinct &&
		len(base) == 10 && len(a.View()) == 10 && reflect.DeepEqual(ag, got)
	report("errors+state", errOK, "4 distinct sentinels; rejected ops leave no trace")
	report("api.SelfCheck", a.SelfCheck() == nil, "four invariants on built-in records")
	// N 个 goroutine 并发同一窗口，结果逐字段一致。
	const N = 16
	cr := make([][]rec.Rec, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); cr[i], _ = a.Compact(0, 10) }(i)
	}
	wg.Wait()
	cok := true
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(cr[i], cr[0]) {
			cok = false
		}
	}
	report("concurrent", cok, "16 goroutines same window; field-identical")
	if fails > 0 {
		os.Exit(1)
	}
}
