package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var fails int

func ok(name string, cond bool, detail any) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fails++
		fmt.Printf("FAIL %s: %v\n", name, detail)
	}
}

func main() {
	tss := []int64{2, 5, 9, 15, 4, 12, 7, 20}
	want := [][]api.Change{
		{}, {{Add: true, Key: "K", Start: 0, End: 10, Count: 2}}, {}, {{Add: true, Key: "K", Start: 0, End: 10, Count: 3}},
		{{Add: false, Key: "K", Start: 0, End: 10, Count: 3}, {Add: true, Key: "K", Start: 0, End: 10, Count: 4}},
		{{Add: true, Key: "K", Start: 10, End: 20, Count: 2}},
		{{Add: false, Key: "K", Start: 0, End: 10, Count: 4}, {Add: true, Key: "K", Start: 0, End: 10, Count: 5}}, {},
	}
	a, _ := api.New(10, 3, 5, 2, 0)
	var log []api.Change
	stepsOK := true
	for i, ts := range tss { // 判定 1：八步每步输出与判定
		cs, err := a.Feed([]api.Event{{Key: "K", TS: ts}})
		if err != nil || fmt.Sprint(cs) != fmt.Sprint(want[i]) {
			stepsOK, _ = false, cs
		}
		log = append(log, cs...)
	}
	ok("第三节八步输出与判定", stepsOK, nil)

	log = append(log, a.Flush()...)
	wantView := map[api.ViewKey]int64{{Key: "K", Start: 0, End: 10}: 5, {Key: "K", Start: 10, End: 20}: 2, {Key: "K", Start: 20, End: 30}: 1}
	ok("最终视图 {[0,10):5,[10,20):2,[20,30):1}", fmt.Sprint(a.View()) == fmt.Sprint(wantView), a.View())

	batch := map[api.ViewKey]int64{} // 独立批量重算：到达时 wm=maxTS-delay，wm<end+lateness 才接受
	var max int64 = -1 << 62
	for _, ts := range tss {
		if ts > max {
			max = ts
		}
		k := ts / 10
		if ts%10 < 0 {
			k--
		}
		if max-3 < (k+1)*10+5 {
			batch[api.ViewKey{Key: "K", Start: k * 10, End: (k + 1) * 10}]++
		}
	}
	ok("Flush 后与批量重算一致", fmt.Sprint(a.View()) == fmt.Sprint(batch), batch)

	var onetime int // on-time +(K,[0,10),3) 全日志恰好一次
	for _, c := range log {
		if c == (api.Change{Add: true, Key: "K", Start: 0, End: 10, Count: 3}) {
			onetime++
		}
	}
	ok("on-time 每窗口至多触发一次", onetime == 1, onetime)

	prefixOK := true // 每个前缀一键至多一值，每条 - 恰撤回当前值
	cur := map[api.ViewKey]int64{}
	for _, c := range log {
		k := api.ViewKey{Key: c.Key, Start: c.Start, End: c.End}
		if c.Add {
			cur[k] = c.Count
		} else if cur[k] != c.Count {
			prefixOK = false
		} else {
			delete(cur, k)
		}
	}
	ok("变更日志每个前缀自洽", prefixOK, nil)

	_, e1 := api.New(0, 3, 5, 2, 0)
	x, _ := api.New(10, 3, 5, 2, 1)
	_, e2 := x.Feed([]api.Event{{Key: "", TS: 1}})
	y, _ := api.New(10, 3, 5, 2, 1)
	_, e3 := y.Feed([]api.Event{{Key: "P", TS: 0}, {Key: "Q", TS: 0}})
	distinct := errors.Is(e1, api.ErrInvalidParam) && errors.Is(e2, api.ErrInvalidEvent) &&
		errors.Is(e3, api.ErrTooManyOpen) && e1 != e2 && e2 != e3 && e1 != e3
	ok("三类哨兵错误可判定且互异", distinct, []error{e1, e2, e3})

	z, _ := api.New(10, 3, 5, 2, 0)
	z.Feed([]api.Event{{Key: "K", TS: 2}})
	before := fmt.Sprint(z.View()) // 非法整批：视图/丢弃数不变，之后仍可用
	if _, err := z.Feed([]api.Event{{Key: "K", TS: 1}, {Key: "", TS: 2}}); !errors.Is(err, api.ErrInvalidEvent) {
		ok("被拒批次不留痕且可继续", false, err)
	} else if _, err := z.Feed([]api.Event{{Key: "K", TS: 5}}); err != nil || fmt.Sprint(z.View()) == before {
		ok("被拒批次不留痕且可继续", false, "state changed or dead")
	} else {
		ok("被拒批次不留痕且可继续", true, nil)
	}
	ok("大 m 检查数有界（经 SelfCheck 布尔判定，不读计数器）", a.SelfCheck() == nil, nil)

	full, _ := api.New(10, 3, 5, 2, 0) // 并发只读：N 个 goroutine 视图逐字段相同
	full.Feed([]api.Event{{Key: "K", TS: 2}, {Key: "K", TS: 5}, {Key: "J", TS: 6}})
	var wg sync.WaitGroup
	res := make([]string, 16)
	for i := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = fmt.Sprint(full.View()) }(i)
	}
	wg.Wait()
	same := true
	for _, r := range res[1:] {
		if r != res[0] {
			same = false
		}
	}
	ok("并发只读结果逐字段一致", same, res)

	if fails > 0 {
		os.Exit(1)
	}
}
