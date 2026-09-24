package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/win"
)

func ev(k string, ts int64) api.Event { return api.Event{Key: k, TS: ts} }
func bag(m map[string]map[int64]int64, k string) map[int64]int64 {
	if m[k] == nil {
		m[k] = map[int64]int64{}
	}
	return m[k]
}

// refBatch 独立重算：只取被接受事件，返回分组计数与期望丢弃数。
func refBatch(s, d, l int64, evs []api.Event) (map[string]map[int64]int64, int64) {
	var wm win.Watermark
	m := map[string]map[int64]int64{}
	var acc int64
	for _, e := range evs {
		w := win.Assign(e.TS, s)
		if win.DropLate(wm.Observe(e.TS, d), w, l) {
			continue
		}
		bag(m, e.Key)[w.Start]++
		acc++
	}
	return m, int64(len(evs)) - acc
}
func asMap(v map[string][]api.WindowCount) map[string]map[int64]int64 {
	m := map[string]map[int64]int64{}
	for k, ws := range v {
		for _, w := range ws {
			bag(m, k)[w.Start] = w.Count
		}
	}
	return m
}

func TestBatchRecompute(t *testing.T) {
	for ci, c := range []struct{ s, d, l int64 }{{10, 3, 5}, {1, 0, 0}, {7, 2, 4}, {3, 5, 1}} {
		r := rand.New(rand.NewSource(int64(ci + 1)))
		evs := make([]api.Event, 60)
		for i := range evs {
			evs[i] = ev(string(rune('a'+r.Intn(5))), int64(r.Intn(81))-30)
		}
		e, _ := api.New(c.s, c.d, c.l, 0)
		e.Feed(evs) // 随机键均合法
		e.Flush()
		want, wantDrop := refBatch(c.s, c.d, c.l, evs)
		if got := asMap(e.View()); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d %v != %v", ci, got, want)
		}
		if e.Dropped() != wantDrop {
			t.Fatalf("case %d dropped=%d want %d", ci, e.Dropped(), wantDrop)
		}
	}
}

func TestChangelogPrefixes(t *testing.T) {
	e, _ := api.New(10, 3, 5, 0)
	cur := map[string]map[int64]int64{}
	apply := func(cs []api.Change) {
		for _, c := range cs {
			if c.Plus {
				bag(cur, c.Key)[c.Start] = c.Count
				continue
			}
			if v, ok := bag(cur, c.Key)[c.Start]; !ok || v != c.Count {
				t.Fatalf("撤回 %+v 现值=%d ok=%v，必须恰好撤回现值", c, v, ok)
			}
			delete(bag(cur, c.Key), c.Start)
		}
	}
	for _, ts := range []int64{2, 9, 15, 4, 18, 5, 22, 7} {
		cs, _ := e.Feed([]api.Event{ev("K", ts)})
		apply(cs)
	}
	apply(e.Flush())
	if !reflect.DeepEqual(asMap(e.View()), cur) {
		t.Fatal("重放全部日志的 View 与逐前缀结果不一致")
	}
}

func TestConcurrentReaders(t *testing.T) {
	e, _ := api.New(10, 3, 5, 0)
	r := rand.New(rand.NewSource(7))
	evs := make([]api.Event, 200)
	for i := range evs {
		evs[i] = ev(string(rune('a'+r.Intn(6))), int64(r.Intn(100)))
	}
	e.Feed(evs) // 随机输入均为合法 Key，不会被拒
	const N = 16
	var wg sync.WaitGroup
	views := make([]map[string][]api.WindowCount, N)
	drops := make([]int64, N)
	oks := make([]bool, N)
	for g := 0; g < N; g++ {
		wg.Add(2)
		go func(g int) { // 高频只读 View/Dropped，竞争由 -race 检查，不靠 sleep
			defer wg.Done()
			for k := 0; k < 50; k++ {
				views[g], drops[g] = e.View(), e.Dropped()
			}
		}(g)
		go func(g int) { defer wg.Done(); oks[g] = api.SelfCheck() == nil }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(views[g], views[0]) || drops[g] != drops[0] || !oks[g] {
			t.Fatalf("goroutine %d 只读结果与 0 不一致", g)
		}
	}
}

func TestParamsAndErrors(t *testing.T) {
	for _, c := range []struct{ s, d, l int64 }{{0, 0, 0}, {-1, 0, 0}, {10, -1, 0}, {10, 0, -2}} {
		if _, err := api.New(c.s, c.d, c.l, 0); !errors.Is(err, api.ErrInvalidParams) {
			t.Fatalf("New(%+v)=%v，应 ErrInvalidParams", c, err)
		}
	}
	if errors.Is(api.ErrEmptyKey, api.ErrMaxOpen) || errors.Is(api.ErrInvalidParams, api.ErrMaxOpen) {
		t.Fatal("三类哨兵必须互不相同")
	}
	e, _ := api.New(10, 1<<40, 0, 1)
	e.Feed([]api.Event{ev("a", 0)})
	v0, d0 := e.View(), e.Dropped()
	for _, b := range [][]api.Event{{ev("", 1)}, {ev("b", 0), ev("c", 10)}} {
		if _, err := e.Feed(b); err == nil {
			t.Fatalf("批次 %+v 应被拒绝", b)
		}
		if !reflect.DeepEqual(e.View(), v0) || e.Dropped() != d0 {
			t.Fatal("被拒批次改变了视图或丢弃数")
		}
	}
	if _, err := e.Feed([]api.Event{ev("a", 1)}); err != nil {
		t.Fatalf("被拒后实例应仍可用：%v", err)
	}
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck：%v", err)
	}
}
