package api

import (
	"errors"
	"math"
	"sync"
	"testing"
)

var seq8 = []op{
	{true, "a", 10, 0}, {true, "b", 20, 1}, {false, "", 0, 10}, {true, "c", 20, 11},
	{false, "", 0, 12}, {false, "", 0, 30}, {true, "d", 25, 31}, {false, "", 0, 40},
}

func step(wm *Watermark, o op) {
	if o.feed {
		wm.Feed(o.key, o.ts, o.pt)
	} else {
		wm.Tick(o.pt)
	}
}

func run(t *testing.T, seq []op) *Watermark {
	wm, _ := New(3, 5)
	for _, o := range seq {
		step(wm, o)
	}
	return wm
}

// 不变量 1：任意序列喂完后，Watermark 等于批量重算结果。
func TestBatchConsistency(t *testing.T) {
	cases := [][]op{
		seq8,
		{{false, "", 0, 5}, {true, "x", 100, 6}, {false, "", 0, 7}},
		{{true, "e", 1000, 0}, {false, "", 0, 4}, {true, "f", 2000, 5}},
	}
	for i, seq := range cases {
		wm := run(t, seq)
		if bwm, bd := replay(3, 5, seq); wm.Watermark() != bwm || wm.Dropped() != bd {
			t.Fatalf("case %d: got %d/%d, batch %d/%d", i, wm.Watermark(), wm.Dropped(), bwm, bd)
		}
	}
	if wm := run(t, seq8); wm.Watermark() != 37 || wm.Dropped() != 1 {
		t.Fatalf("8-step: got %d/%d, want 37/1", wm.Watermark(), wm.Dropped())
	}
}

// 不变量 2：任何 Feed/Tick 都不得让水位回退。
func TestMonotonic(t *testing.T) {
	wm, prev := run(t, nil), int64(math.MinInt64)
	for _, o := range seq8 {
		step(wm, o)
		if got := wm.Watermark(); got < prev {
			t.Fatalf("水位回退: %d -> %d", prev, got)
		}
		prev = wm.Watermark()
	}
}

// 不变量 3：空闲判定与推进只用处理时间，与事件时间无关。
func TestIdleUsesProcessingTime(t *testing.T) {
	wm := run(t, []op{{true, "e", 1000, 0}, {false, "", 0, 4}}) // pt 间隔不足不触发
	if wm.Watermark() != 997 {
		t.Fatalf("pt 间隔不足却触发: wm=%d", wm.Watermark())
	}
	wm2 := run(t, []op{{true, "g", 1, 0}, {false, "", 0, 100}}) // 事件时间仅 1
	if wm2.Watermark() != 97 {                                  // 推进值必须是 100-3
		t.Fatalf("空闲推进用了事件时间: wm=%d, want 97", wm2.Watermark())
	}
}

// 不变量 4：被拒操作不改变任何状态，之后仍可正常使用。
func TestFailureLeavesNoTrace(t *testing.T) {
	wm := run(t, []op{{true, "a", 10, 0}})
	bw, bd := wm.Watermark(), wm.Dropped()
	for i, err := range []error{wm.Feed("", 1, 1), wm.Feed("k", -1, 1), wm.Feed("k", 1, -1), wm.Tick(-1)} {
		if err == nil {
			t.Fatalf("bad op %d 未被拒绝", i)
		}
	}
	if wm.Watermark() != bw || wm.Dropped() != bd {
		t.Fatal("被拒操作改变了状态")
	}
	if err := wm.Feed("b", 20, 1); err != nil || wm.Watermark() != 17 {
		t.Fatal("拒绝后无法继续正常使用")
	}
}

// 故障注入：三类错误可判定且互不相同。
func TestFaultInjection(t *testing.T) {
	_, e1 := New(0, 5)
	wm, _ := New(3, 5)
	e2, e3, e3b := wm.Feed("", 1, 0), wm.Feed("k", -1, 0), wm.Tick(-1)
	ok := errors.Is(e1, ErrNonPositiveParam) && errors.Is(e2, ErrEmptyKey) &&
		errors.Is(e3, ErrNegativeTime) && errors.Is(e3b, ErrNegativeTime)
	if !ok || e1 == e2 || e2 == e3 || e1 == e3 {
		t.Fatal("三类哨兵错误必须可判定且互不相同")
	}
}

// 并发：只读结果一致；推进时水位单调；SelfCheck 可并发。不用 sleep。
func TestConcurrent(t *testing.T) {
	wm := run(t, seq8)
	start, reads := make(chan struct{}), make(chan int64, 64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; reads <- wm.Watermark() }()
	}
	close(start)
	wg.Wait()
	for i := 0; i < 64; i++ {
		if r := <-reads; r != 37 {
			t.Fatalf("并发读不一致: got %d, want 37", r)
		}
	}
	for i := 0; i < 8; i++ { // SelfCheck 并发调用
		wg.Add(1)
		go func() { defer wg.Done(); _ = wm.SelfCheck() }()
	}
	wg.Wait()
	if wm.SelfCheck() != nil {
		t.Fatal("SelfCheck 未通过")
	}
	wm2 := run(t, []op{{true, "s", 100, 0}})
	done, vals := make(chan struct{}), make(chan int64, 4096)
	go func() {
		defer close(vals)
		for {
			select {
			case <-done:
				return
			default:
				vals <- wm2.Watermark()
			}
		}
	}()
	for pt := int64(1); pt <= 5000; pt++ {
		wm2.Tick(pt)
	}
	close(done)
	prev := int64(math.MinInt64)
	for v := range vals {
		if v < prev {
			t.Fatalf("并发推进水位回退: %d -> %d", prev, v)
		}
		prev = v
	}
}
