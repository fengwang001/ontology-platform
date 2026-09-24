package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dwin"
)

func lcg(n, ids, span int, seed int64) []api.Event { // 确定性伪随机乱序序列
	evs, x := make([]api.Event, n), seed
	for i := range evs {
		x = x*6364136223846793005 + 1442695040888963407
		evs[i] = api.Event{ID: fmt.Sprintf("id%d", int(x>>33)%ids), TS: x >> 43 % int64(span)}
	}
	return evs
}

// naive 朴素参照：每 ID 留最近首见 TS（与保留全部历史反向查找等价）。
type naive struct {
	ttl, delay, maxTS int64
	ref               map[string]int64
}

func (n *naive) step(e api.Event) (dup bool, wm int64) {
	if e.TS > n.maxTS {
		n.maxTS = e.TS
	}
	wm = n.maxTS - n.delay
	ts, ok := n.ref[e.ID]
	if dup = ok && !dwin.Expired(wm, ts, n.ttl); !dup {
		n.ref[e.ID] = e.TS
	}
	return dup, wm
}

// runBoth 逐事件跑窗口与参照，钉住判定一致（不变量1）、Mem 精确（2）、wm 单调（3）。
func runBoth(t *testing.T, ttl, delay int64, seq []api.Event) (*api.Window, int64, []api.Event) {
	w, _ := api.New(ttl, delay, 1<<20)
	n := &naive{ttl: ttl, delay: delay, maxTS: int64(dwin.NegInf), ref: map[string]int64{}}
	nd, em, prev := int64(0), []api.Event(nil), int64(dwin.NegInf)
	for i, e := range seq {
		out, err := w.Feed([]api.Event{e})
		if err != nil {
			t.Fatal(err)
		}
		dup, wm := n.step(e)
		want := map[string]int64{}
		for id, ts := range n.ref {
			if !dwin.Expired(wm, ts, n.ttl) {
				want[id] = ts
			}
		}
		if dup != (len(out) == 0) || wm < prev || w.Watermark() != wm ||
			fmt.Sprint(w.Mem()) != fmt.Sprint(want) {
			t.Fatalf("event %d %+v: invariant violated", i, e)
		}
		prev = wm
		if dup {
			nd++
		} else {
			em = append(em, e)
		}
	}
	return w, nd, em
}

func TestNaive(t *testing.T) { // 不变量1：多档随机序列判定/输出与朴素参照一致
	for _, c := range [][3]int64{{10, 2, 7}, {3, 1, 13}, {1, 0, 99}, {20, 5, 2026}} {
		w, nd, em := runBoth(t, c[0], c[1], lcg(300, 5, 40, c[2]))
		if w.Dups() != nd || fmt.Sprint(w.Emitted()) != fmt.Sprint(em) {
			t.Fatalf("case %v: dups/emitted mismatch", c)
		}
	}
}

func TestMemExact(t *testing.T) { // 不变量2：任何时刻 Mem 恰为未过期记忆
	for i, ttl := range []int64{10, 4, 1} {
		runBoth(t, ttl, int64(i), lcg(200, 6, 30, 11)) // runBoth 内逐步比对 Mem
	}
}

func TestWatermarkMonotonic(t *testing.T) { // 不变量3：水位线只进不退
	w, _, _ := runBoth(t, 7, 4, lcg(500, 8, 100, 42)) // runBoth 内逐步断言单调
	fresh, _ := api.New(1, 0, 1)
	if w.Watermark() == int64(dwin.NegInf) || fresh.Watermark() != int64(dwin.NegInf) {
		t.Fatal("watermark boundary violated")
	}
}

func TestRejectedNoTrace(t *testing.T) { // 不变量4：失败不留痕
	for _, c := range [][3]int64{{0, 0, 1}, {1, -1, 1}, {1, 0, 0}, {-5, 0, 3}} {
		if _, err := api.New(c[0], c[1], int(c[2])); !errors.Is(err, api.ErrInvalidConfig) {
			t.Fatalf("New(%v): err=%v", c, err)
		}
	}
	if errors.Is(api.ErrInvalidConfig, api.ErrInvalidEvent) || errors.Is(api.ErrInvalidEvent, api.ErrMemLimit) ||
		errors.Is(api.ErrMemLimit, api.ErrInvalidConfig) {
		t.Fatal("sentinels not distinct")
	}
	w, _ := api.New(1000, 0, 2)
	if _, err := w.Feed([]api.Event{{ID: "a", TS: 0}, {ID: "b", TS: 1}}); err != nil {
		t.Fatal(err)
	}
	saved := stateOf(w)
	cases := [][]api.Event{{{ID: "", TS: 2}}, {{ID: "c", TS: 2}}, {{ID: "a", TS: 3}, {ID: "", TS: 4}}, {{ID: "c", TS: 3}, {ID: "d", TS: 4}}}
	want := []error{api.ErrInvalidEvent, api.ErrMemLimit, api.ErrInvalidEvent, api.ErrMemLimit}
	for i, evs := range cases {
		if _, err := w.Feed(evs); !errors.Is(err, want[i]) || stateOf(w) != saved {
			t.Fatalf("feed %v: rejection violated", evs)
		}
	}
	if _, err := w.Feed([]api.Event{{ID: "a", TS: 1}}); err != nil || w.Dups() != 1 {
		t.Fatal("window unusable after rejections")
	}
}

func stateOf(w *api.Window) string {
	return fmt.Sprintf("%d|%v|%d|%v", w.Watermark(), w.Mem(), w.Dups(), w.Emitted())
}

func TestConcurrentReads(t *testing.T) { // 并发只读结果逐字段相同，无 sleep
	w, _, _ := runBoth(t, 10, 2, lcg(300, 6, 50, 5))
	want := stateOf(w)
	var wg sync.WaitGroup
	bad := make(chan int, 1600)
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				if stateOf(w) != want || w.SelfCheck() != nil {
					bad <- 1
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(bad) != 0 {
		t.Fatal("concurrent reads diverged")
	}
}
