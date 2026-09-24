// Package api 是跳跃窗口计数的对外接口：并发安全，错误均为可判定哨兵。
package api

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"

	"ontology/hagg"
	"ontology/hop"
)

var (
	ErrInvalidParams = errors.New("api: invalid params (need size>0, slide>0, size%slide==0)")
	ErrEmptyKey      = hagg.ErrEmptyKey
	ErrClockBack     = hagg.ErrClockBack
	ErrMaxOpen       = hagg.ErrMaxOpen
)

type Result = hagg.Result

type WindowCounter struct {
	mu  sync.Mutex
	agg *hagg.Agg
}

// New 构造计数器；size/slide 非法返回 ErrInvalidParams。
func New(size, slide int64, maxOpen int) (*WindowCounter, error) {
	if size <= 0 || slide <= 0 || size%slide != 0 {
		return nil, ErrInvalidParams
	}
	return &WindowCounter{agg: hagg.New(size, slide, maxOpen)}, nil
}

// Add 加入事件；空 Key / 超 maxOpen 时被拒且状态不变。
func (w *WindowCounter) Add(key string, ts int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.agg.Add(key, ts)
}

// Advance 推进时钟并返回本次关闭的结果；回退返回 ErrClockBack。
func (w *WindowCounter) Advance(t int64) ([]Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.agg.Advance(t)
}

// Flush 等价于时钟推进到正无穷；Results 返回迄今全部结果（副本）；Dropped 返回丢弃数。
func (w *WindowCounter) Flush() []Result   { w.mu.Lock(); defer w.mu.Unlock(); return w.agg.Flush() }
func (w *WindowCounter) Results() []Result { w.mu.Lock(); defer w.mu.Unlock(); return w.agg.Results() }
func (w *WindowCounter) Dropped() int64    { w.mu.Lock(); defer w.mu.Unlock(); return w.agg.Dropped() }

type ev struct { // 被接受的事件及到达时的时钟（math.MinInt64 表示负无穷）
	key       string
	ts, clock int64
}

func byEndKey(a, b Result) int {
	return cmp.Or(cmp.Compare(a.End, b.End), cmp.Compare(a.Key, b.Key), cmp.Compare(a.Start, b.Start))
}

// naiveRef 朴素参照：对每条被接受事件暴力枚举所属窗口中 end>clock 者计数。
func naiveRef(events []ev, size, slide int64) []Result {
	m := map[[2]any]int64{}
	for _, e := range events {
		for _, s := range hop.Starts(e.ts, size, slide) {
			if s+size > e.clock {
				m[[2]any{e.key, s}]++
			}
		}
	}
	out := make([]Result, 0, len(m))
	for k, c := range m {
		out = append(out, Result{Key: k[0].(string), Start: k[1].(int64), End: k[1].(int64) + size, Count: c})
	}
	slices.SortFunc(out, byEndKey)
	return out
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (w *WindowCounter) SelfCheck() error {
	const size, slide = 12, 4
	fail := func(f string, a ...any) error { return fmt.Errorf("selfcheck: "+f, a...) }
	// 不变量 1+3：确定性伪随机交错序列，Flush 后须等于朴素参照，且有序唯一。
	agg, clock, x := hagg.New(size, slide, 1<<20), int64(math.MinInt64), uint64(42)
	var events []ev
	next := func(n int64) int64 { x = x*6364136223846793005 + 1442695040888963407; return int64(x>>33) % n }
	for i := 0; i < 400; i++ {
		if next(5) < 3 {
			key, ts := string(rune('a'+next(5))), next(100)-50
			if agg.Add(key, ts) == nil {
				events = append(events, ev{key, ts, clock})
			}
		} else if t := next(40) - 20; true {
			if clock != math.MinInt64 && t < clock {
				t = clock + next(20)
			}
			if _, err := agg.Advance(t); err == nil {
				clock = t
			}
		}
	}
	got := func() []Result { agg.Flush(); return agg.Results() }()
	if want := naiveRef(events, size, slide); !slices.Equal(got, want) {
		return fail("mismatch vs naive reference")
	}
	if !slices.IsSortedFunc(got, byEndKey) {
		return fail("output not ordered")
	}
	sameWin := func(a, b Result) bool { return a.Key == b.Key && a.Start == b.Start }
	if len(slices.CompactFunc(got, sameWin)) != len(got) {
		return fail("duplicate output")
	}
	// 不变量 2：时钟从未推进时每条事件恰好计入 size/slide 个窗口。
	a2 := hagg.New(size, slide, 1<<20)
	var total int64
	for i := 0; i < 50; i++ {
		a2.Add("k", int64(i)*3)
	}
	for _, r := range a2.Flush() {
		total += r.Count
	}
	if total != 50*(size/slide) {
		return fail("assignment count %d != %d", total, 50*(size/slide))
	}
	// 不变量 4：四类拒绝均不改变状态。
	a4 := hagg.New(size, slide, 4)
	a4.Add("a", 0) // 3 个窗口，<= maxOpen=4
	before := fmt.Sprint(a4.Results(), a4.Dropped())
	badNew := func() error { _, e := New(0, 4, 1); return e }()
	for i, c := range []struct{ got, want error }{
		{badNew, ErrInvalidParams}, {a4.Add("", 0), ErrEmptyKey}, {a4.Add("b", 0), ErrMaxOpen},
	} {
		if !errors.Is(c.got, c.want) {
			return fail("reject %d: got %v", i, c.got)
		}
	}
	a4.Advance(-100) // 合法推进，为回退铺垫
	if _, err := a4.Advance(-200); !errors.Is(err, ErrClockBack) {
		return fail("clock back: %v", err)
	}
	if fmt.Sprint(a4.Results(), a4.Dropped()) != before {
		return fail("rejected op changed state")
	}
	return nil
}
