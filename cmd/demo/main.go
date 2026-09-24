// Command demo 逐条打印事件时间去重窗口的自检结论，退出码 0 表示全部 OK。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dedup"
	"ontology/dwin"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
		fails++
	}
}

// 第三节十步事件。
var tenEvents = []api.Event{
	{ID: "a", TS: 5}, {ID: "b", TS: 8}, {ID: "a", TS: 9}, {ID: "c", TS: 17}, {ID: "a", TS: 14},
	{ID: "b", TS: 20}, {ID: "d", TS: 4}, {ID: "d", TS: 6}, {ID: "c", TS: 26}, {ID: "a", TS: 25},
}

// naive 是 demo 本地的朴素参照：每 ID 留最近首见，按同一过期条件判定。
func naive(ttl, delay int64, seq []api.Event) (nd int64, mem map[string]int64, em []api.Event) {
	ref := map[string]int64{}
	maxTS := int64(dwin.NegInf)
	for _, e := range seq {
		if e.TS > maxTS {
			maxTS = e.TS
		}
		if ts, ok := ref[e.ID]; ok && !dwin.Expired(maxTS-delay, ts, ttl) {
			nd++
			continue
		}
		ref[e.ID] = e.TS
		em = append(em, e)
	}
	mem = map[string]int64{}
	for id, ts := range ref {
		if !dwin.Expired(maxTS-delay, ts, ttl) {
			mem[id] = ts
		}
	}
	return nd, mem, em
}

func main() {
	w := dwin.New(2)
	wms := make([]int64, len(tenEvents))
	for i, e := range tenEvents {
		wms[i] = w.Observe(e.TS)
	}
	check("dwin: 十步水位线 3,6,7,15,15,18,18,18,24,24；第4步恰清a(15=5+10)",
		fmt.Sprint(wms) == "[3 6 7 15 15 18 18 18 24 24]" &&
			dwin.Expired(15, 5, 10) && !dwin.Expired(14, 5, 10))

	d := dedup.New(10, 2)
	dups := make([]bool, 10)
	for i, e := range tenEvents {
		dups[i] = d.Process(e.ID, e.TS)
	}
	check("dedup: 十步判定 新新重新新新新新重新；dups=2；第10步后 a25,b20,c17",
		fmt.Sprint(dups) == "[false false true false false false false false true false]" &&
			d.Dups() == 2 && fmt.Sprint(d.Mem()) == "map[a:25 b:20 c:17]")

	e1 := errors.Is(mustErr(api.New(0, 0, 1)), api.ErrInvalidConfig) &&
		errors.Is(mustErr(api.New(1, -1, 1)), api.ErrInvalidConfig) &&
		errors.Is(mustErr(api.New(1, 0, 0)), api.ErrInvalidConfig)
	w2, _ := api.New(1000, 0, 2)
	w2.Feed([]api.Event{{ID: "a", TS: 0}, {ID: "b", TS: 1}})
	_, errEmpty := w2.Feed([]api.Event{{ID: "", TS: 2}})
	_, errFull := w2.Feed([]api.Event{{ID: "c", TS: 2}})
	distinct := !errors.Is(api.ErrInvalidConfig, api.ErrInvalidEvent) &&
		!errors.Is(api.ErrInvalidEvent, api.ErrMemLimit) &&
		!errors.Is(api.ErrMemLimit, api.ErrInvalidConfig)
	check("api: 三类错误可判定且互不相同", e1 && distinct &&
		errors.Is(errEmpty, api.ErrInvalidEvent) && errors.Is(errFull, api.ErrMemLimit))

	saved := fmt.Sprint(w2.Emitted(), w2.Mem(), w2.Dups(), w2.Watermark())
	w2.Feed([]api.Event{{ID: "x", TS: 5}, {ID: "", TS: 6}}) // 整批应被拒
	w2.Feed([]api.Event{{ID: "c", TS: 7}})                  // 超限应被拒
	unchanged := fmt.Sprint(w2.Emitted(), w2.Mem(), w2.Dups(), w2.Watermark()) == saved
	_, eAfter := w2.Feed([]api.Event{{ID: "a", TS: 1}}) // 拒绝后仍可正常使用
	check("api: 被拒后状态不变且之后仍可用", unchanged && eAfter == nil && w2.Dups() == 1)

	seq := make([]api.Event, 200) // 确定性伪随机乱序到达
	x := int64(7)
	for i := range seq {
		x = x*6364136223846793005 + 1442695040888963407
		seq[i] = api.Event{ID: fmt.Sprintf("id%d", int(x>>33)%7), TS: x >> 43 % 50}
	}
	w3, _ := api.New(5, 3, 1<<20)
	for _, e := range seq {
		w3.Feed([]api.Event{e})
	}
	nd, mem, em := naive(5, 3, seq)
	check("api: 随机序列与朴素参照一致",
		w3.Dups() == nd && fmt.Sprint(w3.Mem()) == fmt.Sprint(mem) &&
			fmt.Sprint(w3.Emitted()) == fmt.Sprint(em))

	check("api: SelfCheck 通过", mustOK(w3.SelfCheck()))
	check("dedup: 大m下检查条数不随m增长", dedup.SelfCheck() == nil)

	want := fmt.Sprint(w3.Emitted(), w3.Mem(), w3.Dups(), w3.Watermark())
	var wg sync.WaitGroup
	start := make(chan struct{})
	bad := make(chan struct{}, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if fmt.Sprint(w3.Emitted(), w3.Mem(), w3.Dups(), w3.Watermark()) != want || w3.SelfCheck() != nil {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("api: 16 goroutine 并发只读结果逐字段相同", len(bad) == 0)

	if fails > 0 {
		os.Exit(1)
	}
}

func mustErr(_ *api.Window, err error) error { return err }
func mustOK(err error) bool                  { return err == nil }
