package wagg

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/win"
)

func batchRef(evs []Event, size, delay, late int64) map[ViewKey]int64 {
	mx, v := int64(-1<<62), map[ViewKey]int64{}
	for _, e := range evs {
		mx = max(mx, e.TS)
		if g := win.Assign(e.TS, size); mx-delay < g.End+late {
			v[ViewKey{e.Key, g.Start, g.End}]++
		}
	}
	return v
}
func rndEvs(n int, span, base int64, keys int, seed int64) []Event {
	r, evs := rand.New(rand.NewSource(seed)), make([]Event, n)
	for i := range evs {
		evs[i] = Event{string(rune('A' + r.Intn(keys))), r.Int63n(span) - base}
	}
	return evs
}
func runAgg(c [4]int64, evs []Event) *Agg {
	a, _ := New(c[0], c[1], c[2], c[3], 0)
	a.Feed(evs)
	a.Flush()
	return a
}
func TestBatchRecompute(t *testing.T) {
	for ci, c := range [][4]int64{{10, 3, 5, 2}, {7, 2, 4, 3}} {
		for seed := 0; seed < 3; seed++ {
			evs := rndEvs(60, 80, 40, 4, int64(ci*8+seed))
			if !reflect.DeepEqual(runAgg(c, evs).View(), batchRef(evs, c[0], c[1], c[2])) {
				t.Fatal("mismatch", ci, seed)
			}
		}
	}
	// 保守预校验会拒（涉及 3 个新窗 >maxOpen=1）；精确模拟峰值恒 1 应接受，且 K@15 入已 purge 的 [10,20) 被丢弃。
	b, _ := New(10, 0, 0, 1, 1)
	if _, e := b.Feed([]Event{{"K", 0}, {"K", 20}, {"K", 15}, {"K", 21}}); e != nil || b.Dropped() != 1 {
		t.Fatal(e, b.Dropped())
	}
}
func TestChangelogPrefixes(t *testing.T) {
	for seed := int64(0); seed < 6; seed++ {
		r := rand.New(rand.NewSource(seed + 100))
		a, _ := New(1+int64(r.Intn(8)), r.Int63n(5), r.Int63n(6), 1+int64(r.Intn(3)), 0)
		var log []Change
		for _, e := range rndEvs(80, 100, 50, 5, seed) {
			cs, _ := a.Feed([]Event{e})
			log = append(log, cs...)
		}
		log = append(log, a.Flush()...)
		cur := map[ViewKey]int64{}
		for _, c := range log { // 每条 - 必须命中存在且相等的当前值；map 保证一键至多一值
			k := ViewKey{c.Key, c.Start, c.End}
			if c.Add {
				cur[k] = c.Count
			} else if v := cur[k]; v != c.Count {
				t.Fatal("bad minus", c, v)
			} else {
				delete(cur, k)
			}
		}
	}
}
func TestOnTimeOnce(t *testing.T) {
	a, _ := New(10, 0, 0, 100, 0)
	if cs, _ := a.Feed([]Event{{"K", 5}}); len(cs) != 0 { // early=100：TS=5 无快照
		t.Fatalf("early leaked %v", cs)
	}
	var log []Change
	for _, ts := range []int64{15, 16, 20, 100} { // wm 多次越过 end=10
		cs, _ := a.Feed([]Event{{"K", ts}})
		log = append(log, cs...)
	}
	same := func(c Change) bool { return c == Change{true, "K", 0, 10, 1} }
	i := slices.IndexFunc(log, same)
	if i < 0 || slices.IndexFunc(log[i+1:], same) >= 0 {
		t.Fatalf("on-time not exactly once: %v", log)
	}
	b, _ := New(10, 2, 0, 100, 0)
	b.Feed([]Event{{"K", 50}, {"K", 1}})
	if b.wm != 48 { // 不变量 3：wm 不回退
		t.Fatalf("wm=%d", b.wm)
	}
}
func TestRejectedBatchAtomic(t *testing.T) {
	for _, p := range [][4]int64{{0, 1, 1, 1}, {10, -1, 0, 1}, {10, 0, -1, 1}, {10, 0, 0, 0}} {
		if _, err := New(p[0], p[1], p[2], p[3], 0); err != ErrInvalidParam {
			t.Fatalf("param %v: %v", p, err)
		}
	}
	a, _ := New(10, 3, 5, 2, 1)
	a.Feed([]Event{{"K", 2}, {"K", 5}})
	bad := [][]Event{{{"K", 1}, {"", 2}}, {{"P", 0}, {"Q", 10}}}
	want := []error{ErrInvalidEvent, ErrTooManyOpen}
	for i := range bad {
		s := fmt.Sprintf("%d|%d|%d|%d", len(a.log), a.dropped, a.wm, len(a.open))
		if _, err := a.Feed(bad[i]); err != want[i] {
			t.Fatal(i, err)
		}
		if now := fmt.Sprintf("%d|%d|%d|%d", len(a.log), a.dropped, a.wm, len(a.open)); now != s { // 计数/wm/丢弃数/日志全部不变
			t.Fatal("rejected batch left trace")
		}
	}
	if _, err := a.Feed([]Event{{"K", 9}}); err != nil { // 被拒后仍可正常使用
		t.Fatal(err)
	}
}
func TestInspectionSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, _ := New(1, 1<<60, 0, 100, 0) // 极大 delay：m 个窗口全部保活不触发
		for i := 0; i < m; i++ {
			a.Feed([]Event{{fmt.Sprintf("k%d", i), 0}})
		}
		a.Feed([]Event{{"probe", 1}}) // wm 只进 1，零触发零清除
		if a.inspected > 2 {          // 与 m 无关的小常数
			t.Fatalf("m=%d inspected=%d", m, a.inspected)
		}
	}
}
func TestConcurrentReaders(t *testing.T) {
	a, _ := New(10, 3, 5, 2, 0)
	a.Feed(rndEvs(200, 100, 0, 6, 7))
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	const N = 24
	var wg sync.WaitGroup
	vs, ds := make([]string, N), make([]int64, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); vs[i], ds[i] = fmt.Sprint(a.View()), a.Dropped(); _ = a.SelfCheck() }(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if vs[i] != vs[0] || ds[i] != ds[0] {
			t.Fatal("concurrent reads differ")
		}
	}
}
