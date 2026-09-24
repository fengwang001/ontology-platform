package main

import (
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/cwin"
)

var pass = true

func ck(name string, ok bool) {
	if !ok {
		pass = false
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[ok] + name)
}

// want 是第三节九行表中八步与 Flush 的预期输出（Key=K）。
var want = [][]api.Out{
	{{Key: "K", Start: -12, End: -8, Count: 0}},
	{{Key: "K", Start: -12, End: -4, Count: 1}},
	nil,
	{{Key: "K", Start: -12, End: 0, Count: 2}, {Key: "K", Start: 0, End: 4, Count: 1}},
	nil,
	{{Key: "K", Start: 0, End: 8, Count: 2}},
	nil,
	{{Key: "K", Start: 0, End: 12, Count: 3}, {Key: "K", Start: 12, End: 16, Count: 1}},
}

func main() {
	ck("cwin 负时间戳向下取整归属与 Emin", negStart())
	a, _ := api.New(12, 4, 2, 2)
	stepOK := true
	ts := []int64{-5, 1, -2, 6, 3, 11, 12, 19}
	for i, t := range ts {
		o, e := a.Feed([]api.Event{{Key: "K", TS: t}})
		stepOK = stepOK && e == nil && reflect.DeepEqual(o, want[i])
	}
	f := a.Flush()
	stepOK = stepOK && reflect.DeepEqual(f,
		[]api.Out{{Key: "K", Start: 12, End: 20, Count: 2}, {Key: "K", Start: 12, End: 24, Count: 2}})
	ck("第三节八事件+Flush 每步输出与九行表一致", stepOK)
	ck("第5步 wm 恰等于 Emin 时丢弃(Dropped=1)", a.Dropped() == 1)
	ck("Flush 后与批量重算一致/累积单调/SelfCheck 四条", a.SelfCheck() == nil)
	ck("三类哨兵错误可判定且互不相同", distinctErrors())
	ck("被拒整批不留痕且之后仍可正常使用", rejectNoTrace())
	ck("大 m 下扫描有序定位(scan 白盒断言见 TestComplexityScansSublinear)", bigM())
	ck("并发只读 All/Dropped/SelfCheck 结果逐字段一致", concurrentRead())
	if !pass {
		panic("demo failed")
	}
}

func negStart() bool {
	sp, e := cwin.New(12, 4, 2)
	return e == nil &&
		sp.Start(-5) == -12 && sp.Start(-13) == -24 && sp.Start(-12) == -12 &&
		sp.Start(11) == 0 && sp.Start(12) == 12 && // 边界左闭右开
		sp.EMin(-12, -5) == -4 && sp.EMin(0, 0) == 4
}

func distinctErrors() bool {
	return api.ErrInvalidParams != api.ErrEmptyKey &&
		api.ErrInvalidParams != api.ErrTooManyOpenWindows &&
		api.ErrEmptyKey != api.ErrTooManyOpenWindows
}

func rejectNoTrace() bool {
	z, _ := api.New(12, 4, 100, 1)
	b0, d0 := z.All(), z.Dropped()
	_, e1 := z.Feed([]api.Event{{Key: "", TS: 1}})
	_, e2 := z.Feed([]api.Event{{Key: "K", TS: 1}, {Key: "K", TS: 13}})
	_, e3 := z.Feed([]api.Event{{Key: "K", TS: 1}})
	return e1 == api.ErrEmptyKey && e2 == api.ErrTooManyOpenWindows && e3 == nil &&
		reflect.DeepEqual(z.All(), b0) && z.Dropped() == d0
}

func bigM() bool {
	// 功能级：多档 m 下大 delay 保持 m 窗存活，wm 前进 1 不触发任何子窗口。
	for _, m := range []int{100, 1000, 10000} {
		a, _ := api.New(1<<30, 1<<29, 1<<40, m+1)
		evs := make([]api.Event, m)
		for i := range evs {
			evs[i] = api.Event{Key: fmt.Sprintf("k%05d", i), TS: 1}
		}
		if _, e := a.Feed(evs); e != nil { // 全部落在不同 (Key,大窗口)
			return false
		}
		o, e := a.Feed([]api.Event{{Key: "probe", TS: 2}}) // wm 仅前进 1
		if e != nil || len(o) != 0 {
			return false
		}
	}
	return true
}

func concurrentRead() bool {
	a, _ := api.New(12, 4, 2, 4)
	for _, t := range []int64{-5, 1, 6, 11, 12, 19} {
		if _, e := a.Feed([]api.Event{{Key: "K", TS: t}}); e != nil {
			return false
		}
	}
	a.Flush()
	base := a.All()
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := a.All()
			d := a.Dropped()
			e := a.SelfCheck()
			mu.Lock()
			ok = ok && reflect.DeepEqual(g, base) && d == 0 && e == nil
			mu.Unlock()
		}()
	}
	wg.Wait()
	return ok
}
