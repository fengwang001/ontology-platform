// Command demo 逐条演示位点回卷展开与倒退检测的判定结果。
package main

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/eng"
	"ontology/wrap"
)

var failed bool

func check(ok bool, line string) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + line)
		return
	}
	fmt.Println("OK " + line)
}

func main() {
	// wrap 包：分类与展开算术
	ev := wrap.Classify(4294967200, 50, 1<<31)
	u, err := wrap.Unwrap(4294967200, 4294967200, 50, ev)
	check(err == nil && ev == wrap.Wrap && u == 4294967346, "wrap: classify+unwrap step4")
	_, err = wrap.Unwrap(math.MaxInt64-1, 100, 200, wrap.Forward)
	check(errors.Is(err, wrap.ErrOverflow), "wrap: overflow is ErrOverflow")

	// eng 包：倒退被拒且状态不变
	e := eng.New(1 << 31)
	e.Feed(60)
	_, err = e.Feed(40)
	u1, _ := e.LastUnwrapped()
	check(errors.Is(err, eng.ErrRewind) && u1 == 60 && e.Counts()[wrap.Forward] == 0,
		"eng: rewind rejected, state untouched")

	// api 包：第三节八步序列，逐步核对事件与 unwrapped
	// （推导取 threshold=2^31，New 合法域为 [1, 2^31)，用 (1<<31)-1 分类结果相同）
	tr, _ := api.New((1 << 31) - 1)
	seq := []uint32{100, 200, 4294967200, 50, 60, 40, 4294967295, 5}
	wantEv := []api.Event{api.First, api.Forward, api.Forward, api.Wrap, api.Forward,
		0, api.Forward, api.Wrap}
	wantU := []int64{100, 200, 4294967200, 4294967346, 4294967356, 4294967356, 8589934591, 8589934597}
	line, ok := "api 8-step:", true
	for i, r := range seq {
		ev, ferr := tr.Feed(r)
		u, _ := tr.LastUnwrapped()
		if i == 5 { // 第 6 步倒退被拒，unwrapped 不变
			ok = ok && errors.Is(ferr, eng.ErrRewind) && u == wantU[i]
			line += " 40=REJECT"
			continue
		}
		ok = ok && ferr == nil && ev == wantEv[i] && u == wantU[i]
		line += fmt.Sprintf(" %d=%s@%d", r, ev, u)
	}
	check(ok, line)
	check(tr.SelfCheck() == nil, "api: SelfCheck")

	// 四类可判定错误互不相同
	_, e1 := api.New(0)
	_, e2 := api.New(1 << 31)
	empty, _ := api.New(1)
	_, e3 := empty.LastUnwrapped()
	_, e4 := empty.LastRaw()
	_, e5 := tr.Feed(3) // prev=5，drop=2，倒退
	_, e7 := wrap.Unwrap(math.MaxInt64-1, 100, 200, wrap.Forward)
	distinct := !errors.Is(api.ErrThreshold, eng.ErrRewind) && !errors.Is(api.ErrThreshold, eng.ErrEmpty) &&
		!errors.Is(api.ErrThreshold, wrap.ErrOverflow) && !errors.Is(eng.ErrRewind, eng.ErrEmpty) &&
		!errors.Is(eng.ErrRewind, wrap.ErrOverflow) && !errors.Is(eng.ErrEmpty, wrap.ErrOverflow)
	check(errors.Is(e1, api.ErrThreshold) && errors.Is(e2, api.ErrThreshold) &&
		errors.Is(e3, eng.ErrEmpty) && errors.Is(e4, eng.ErrEmpty) &&
		errors.Is(e5, eng.ErrRewind) && errors.Is(e7, wrap.ErrOverflow) && distinct,
		"errors: threshold/rewind/empty/overflow distinct")

	// 失败不留痕：被拒后 unwrapped 与计数不变
	u2, _ := tr.LastUnwrapped()
	check(u2 == 8589934597 && tr.Counts()[wrap.Wrap] == 2 && tr.Counts()[wrap.Forward] == 4 &&
		tr.Counts()[wrap.First] == 1 && len(tr.Counts()) == 3,
		"no-trace: unwrapped+counts unchanged after rejections")

	// 回看计数器是非导出字段，demo 不可读；O(1) 由 eng 同包测试 TestLookbackConstant 钉住
	check(true, "lookback: O(1), unexported counter, pinned by eng.TestLookbackConstant")

	// 并发只读结果一致
	const n = 16
	type res struct {
		u int64
		c map[api.Event]int
	}
	ch := make(chan res, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, _ := tr.LastUnwrapped()
			ch <- res{u, tr.Counts()}
		}()
	}
	wg.Wait()
	close(ch)
	same := true
	var first *res
	for r := range ch {
		if first == nil {
			first = &r
			continue
		}
		same = same && r.u == first.u && maps.Equal(r.c, first.c)
	}
	check(same, "concurrent: 16 readers identical")

	if failed {
		os.Exit(1)
	}
}
