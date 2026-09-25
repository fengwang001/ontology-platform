// Command demo 演示带水位线与闭合判定的会话窗口合并。
// 不读参数、不联网；逐条打印 OK/FAIL，退出码 0 表示全部通过。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"

	"ontology/api"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

type trip struct{ s, e, c int64 }

// batch 是独立写出的批量重算：只取被接受事件，按 TS 升序、间隔 <= gap 连通。
func batch(gap int64, ts []int64) []trip {
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	var out []trip
	for _, t := range ts {
		if n := len(out); n > 0 && t-out[n-1].e <= gap {
			out[n-1].e, out[n-1].c = t, out[n-1].c+1
		} else {
			out = append(out, trip{t, t, 1})
		}
	}
	return out
}

func main() {
	seq := []api.Event{{Key: "K", TS: 10}, {Key: "K", TS: 13}, {Key: "K", TS: 20}, {Key: "K", TS: 16},
		{Key: "K", TS: 23}, {Key: "K", TS: 17}, {Key: "K", TS: 25}, {Key: "K", TS: 11}}
	want := []trip{{10, 10, 1}, {10, 13, 2}, {20, 20, 1}, {}, {20, 23, 2}, {17, 23, 3}, {17, 25, 4}, {}}
	a, err := api.New(3, 100)
	if err != nil {
		panic(err)
	}
	stepsOK := true
	for i, ev := range seq {
		got, err := a.Feed([]api.Event{ev})
		if err != nil {
			stepsOK = false
			break
		}
		if want[i] == (trip{}) {
			stepsOK = stepsOK && got[0] == api.Session{} // 第4、8步：丢弃
			continue
		}
		stepsOK = stepsOK && got[0].Start == want[i].s && got[0].End == want[i].e && got[0].Count == want[i].c
	}
	report("八步动作正确(第2步==gap合并/第6步反向扩展)", stepsOK)

	view := a.View()["K"]
	gotTrips := make([]trip, len(view))
	for i, s := range view {
		gotTrips[i] = trip{s.Start, s.End, s.Count}
	}
	report("View 与批量重算一致", reflect.DeepEqual(gotTrips, batch(3, []int64{10, 13, 20, 23, 17, 25})) &&
		a.Dropped() == 2 && reflect.DeepEqual(view, []api.Session{{Start: 10, End: 13, Count: 2}, {Start: 17, End: 25, Count: 4}}))

	// 闭合不可变：推进到超高水位线并补一事件后，[10,13] 三元组必须原样。
	frozen := view[0]
	_, _ = a.Feed([]api.Event{{Key: "K", TS: 1_000_000}, {Key: "K", TS: 12}})
	report("闭合会话冻结不可变", a.View()["K"][0] == frozen)

	badGap := errors.Is(func() error { _, e := api.New(0, 1); return e }(), api.ErrBadGap)
	r, _ := api.New(3, 1)
	_, _ = r.Feed([]api.Event{{Key: "A", TS: 1}}) // 占住唯一开放名额
	before := r.View()
	_, eLim := r.Feed([]api.Event{{Key: "B", TS: 2}})
	_, eKey := r.Feed([]api.Event{{Key: "", TS: 2}})
	report("三类可判定错误互不相同", badGap && errors.Is(eLim, api.ErrTooManyOpen) && errors.Is(eKey, api.ErrEmptyKey))
	report("被拒整批不留痕且可续用", r.Dropped() == 0 && reflect.DeepEqual(r.View(), before) &&
		func() bool { _, e := r.Feed([]api.Event{{Key: "A", TS: 2}}); return e == nil }())
	report("SelfCheck 与大m比较数有界", a.SelfCheck() == nil)

	// 并发只读：N 个 goroutine 同时读同一已喂满实例，视图必须逐字段相同（WaitGroup 同步，无 sleep）。
	const n = 32
	res := make([]map[string][]api.Session, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i] = a.View()
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && reflect.DeepEqual(res[0], res[i])
	}
	report("并发只读视图逐字段一致", same)

	if failed {
		os.Exit(1)
	}
}
