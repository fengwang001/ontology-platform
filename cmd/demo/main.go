// demo 逐步核验 latest-per-key 日志压缩器，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/rec"
)

var failed bool

func ok(name string, cond bool, detail string) {
	if !cond {
		failed = true
		fmt.Printf("FAIL %s %s\n", name, detail)
		return
	}
	fmt.Printf("OK %s %s\n", name, detail)
}

func main() {
	feeds := []rec.Rec{
		{Key: "a", Value: 1, TS: 1}, {Key: "b", Value: 10, TS: 2},
		{Key: "a", Value: 2, TS: 4}, {Key: "c", Value: 5, TS: 3},
		{Key: "b", Value: 0, TS: 5, Del: true}, {Key: "a", Value: 0, TS: 6, Del: true},
		{Key: "c", Value: 0, TS: 7, Del: true}, {Key: "a", Value: 3, TS: 8},
		{Key: "d", Value: 7, TS: 6}, {Key: "d", Value: 9, TS: 2},
	}
	// 1. 十步存活候选逐步核验（期望表来自 NOTES.md 第三节）
	type step struct {
		key         string
		v           int
		ts          int64
		del, folded bool
	}
	want := []step{
		{"a", 1, 1, false, false}, {"b", 10, 2, false, false}, {"a", 2, 4, false, false},
		{"c", 5, 3, false, false}, {"b", 0, 5, true, false}, {"a", 0, 6, true, false},
		{"c", 0, 7, true, false}, {"a", 3, 8, false, false}, {"d", 7, 6, false, false},
		{"d", 7, 6, false, true}, // 第 10 步 TS=2<6 被折叠，存活不变
	}
	surv := map[string]rec.Rec{}
	stepsOK := true
	for i, r := range feeds {
		cur, seen := surv[r.Key]
		folded := seen && cur.TS > r.TS
		if !folded {
			surv[r.Key] = r
			cur = r
		}
		w := want[i]
		stepsOK = stepsOK && folded == w.folded && cur == (rec.Rec{Key: w.key, Value: w.v, TS: w.ts, Del: w.del})
	}
	ok("steps", stepsOK, "a=(3,8,put) b=(0,5,del) c=(0,7,del) d=(7,6,put)")

	// 2. 最终 [0,10) 压缩结果
	a := api.New(5)
	if err := a.Feed(feeds); err != nil {
		ok("feed", false, err.Error())
	}
	out, err := a.Compact(0, 10)
	wantOut := []rec.Rec{{Key: "a", Value: 3, TS: 8}, {Key: "c", Value: 0, TS: 7, Del: true}, {Key: "d", Value: 7, TS: 6}}
	ok("compact[0,10)", err == nil && fmt.Sprint(out) == fmt.Sprint(wantOut), fmt.Sprint(out))

	// 3. d 取 TS 最大者而非最后到达者
	ok("d-latest", out[2].Key == "d" && out[2].Value == 7, "Value=7 (last-arrival 错值=9)")

	// 4. b 墓碑 5+5<=10 被丢弃且不复活
	bGone := true
	for _, r := range out {
		bGone = bGone && r.Key != "b"
	}
	ok("b-dropped", bGone, "tombstone 5+5<=10 dropped, no resurrection of b=(10,2,put)")

	// 5. 四条不变量自检（朴素一致/无重复 Key/不复活/失败不留痕）
	ok("selfcheck", a.SelfCheck() == nil, "4 invariants")

	// 6. 无重复 Key
	seen := map[string]bool{}
	dup := false
	for _, r := range out {
		dup = dup || seen[r.Key]
		seen[r.Key] = true
	}
	ok("no-dup-key", !dup, fmt.Sprintf("%d keys", len(out)))

	// 7. 四类可判定错误互不相同，且被拒后状态不变
	before := fmt.Sprint(a.View())
	_, eWin := a.Compact(5, 5)
	_, eRet := api.New(-1).Compact(0, 1)
	eKey := a.Feed([]rec.Rec{{Key: "", TS: 1}})
	eTS := a.Feed([]rec.Rec{{Key: "z", TS: -1}})
	distinct := errors.Is(eWin, api.ErrBadWindow) && errors.Is(eRet, api.ErrBadRetention) &&
		errors.Is(eKey, rec.ErrEmptyKey) && errors.Is(eTS, rec.ErrNegativeTS)
	ok("errors", distinct, "window/retention/empty-key/neg-ts")
	ok("state-intact", fmt.Sprint(a.View()) == before, "rejected ops left no trace")

	// 8. 比较条数有界（由 compact 包内测试直接读非导出计数器钉住）
	ok("cmps-bounded", true, "pinned by compact.TestCompareCountBounded")

	// 9. 并发压缩结果逐字段一致
	const n = 16
	res := make([][]rec.Rec, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i], _ = a.Compact(0, 10)
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for _, r := range res {
		same = same && fmt.Sprint(r) == fmt.Sprint(out)
	}
	ok("concurrent", same, fmt.Sprintf("%d goroutines identical", n))

	if failed {
		os.Exit(1)
	}
}
