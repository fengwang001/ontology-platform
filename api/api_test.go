package api

import (
	"errors"
	"sync"
	"testing"

	"ontology/wm"
)

type ev struct {
	hb  bool
	key string
	ts  int64
}

var eightSteps = []ev{
	{false, "a", 5}, {true, "", 12}, {false, "b", 8}, {true, "", 9},
	{true, "", 20}, {false, "c", 14}, {true, "", 17}, {true, "", 25},
}

func feed(t *testing.T, e *Engine, x ev) {
	t.Helper()
	if x.hb {
		if err := e.Heartbeat(x.ts); err != nil {
			t.Fatal(err)
		}
	} else if err := e.Feed(x.key, x.ts); err != nil {
		t.Fatal(err)
	}
}

// gate 用开闸 channel 让 n 个 goroutine 真正同时执行 f（不用 sleep 制造时序）。
func gate(n int, f func(int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; f(i) }(i)
	}
	close(start)
	wg.Wait()
}
func TestBatchRecomputeConsistency(t *testing.T) {
	// 钉住不变量1：流式结果 == 数据(TS-delay)与心跳(TS)整体取 max 的批量重算。
	for _, c := range []struct {
		delay int64
		evs   []ev
	}{
		{3, eightSteps},
		{1, []ev{{false, "k", 4}, {true, "", 3}, {false, "j", 10}, {true, "", 30}}},
	} {
		e, _ := New(c.delay)
		batch := wm.NegInfinity
		for _, x := range c.evs {
			feed(t, e, x)
			v := x.ts
			if !x.hb {
				v -= c.delay
			}
			batch = max(batch, v)
		}
		if e.Watermark() != batch {
			t.Fatalf("wm=%d batch=%d", e.Watermark(), batch)
		}
	}
}
func TestWatermarkMonotonic(t *testing.T) {
	// 钉住不变量2：任何事件（含迟到心跳）都不得让水位回退。
	e, _ := New(3)
	prev := wm.NegInfinity
	for _, x := range append(eightSteps, ev{true, "", 1}) {
		feed(t, e, x)
		if e.Watermark() < prev {
			t.Fatalf("retreat after %+v: %d", x, prev)
		}
		prev = e.Watermark()
	}
}
func TestEightStepSequence(t *testing.T) {
	// 钉住不变量3：心跳贡献 TS、数据贡献 TS-delay、迟到心跳不改水位只计数。
	e, _ := New(3)
	want := [][2]int64{{2, 0}, {12, 0}, {12, 0}, {12, 1}, {20, 1}, {20, 1}, {20, 2}, {25, 2}}
	for i, x := range eightSteps {
		feed(t, e, x)
		if e.Watermark() != want[i][0] || e.LateHeartbeats() != want[i][1] {
			t.Fatalf("row %d wm=%d late=%d want=%v", i+1, e.Watermark(), e.LateHeartbeats(), want[i])
		}
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	// 钉住不变量4：三类拒绝可判定、互不相同、不改状态、之后仍可用。
	if bad, err := New(0); bad != nil || !errors.Is(err, ErrInvalidDelay) {
		t.Fatal("New(0) must fail with ErrInvalidDelay")
	}
	e, _ := New(3)
	if err := e.Feed("k", 10); err != nil {
		t.Fatal(err)
	}
	w0, l0 := e.Watermark(), e.LateHeartbeats()
	for i, fn := range []func() error{
		func() error { return e.Feed("", 5) },
		func() error { return e.Feed("k", -1) },
		func() error { return e.Heartbeat(-1) },
	} {
		want := []error{ErrEmptyKey, ErrNegativeTS, ErrNegativeTS}[i]
		if err := fn(); !errors.Is(err, want) {
			t.Fatalf("got %v want %v", err, want)
		}
	}
	if e.Watermark() != w0 || e.LateHeartbeats() != l0 {
		t.Fatal("rejected events changed state")
	}
	if err := e.Heartbeat(99); err != nil || e.Watermark() != 99 {
		t.Fatal("engine unusable after rejection")
	}
}
func TestConcurrentAdvanceMonotonic(t *testing.T) {
	// 并发只读同一已喂满实例水位完全相同；并发推进时每个 goroutine 连续观测到的水位单调不减。无 sleep。
	e, _ := New(1)
	for _, x := range eightSteps {
		feed(t, e, x)
	}
	reads := make([]int64, 32)
	gate(32, func(i int) { reads[i] = e.Watermark() })
	for _, v := range reads {
		if v != reads[0] {
			t.Fatal("concurrent readers disagree")
		}
	}
	gate(16, func(g int) {
		prev := wm.NegInfinity
		for i := 0; i < 200; i++ {
			ts := int64(g*200 + i)
			if i%2 == 0 {
				_ = e.Feed("k", ts)
			} else {
				_ = e.Heartbeat(ts)
			}
			w := e.Watermark()
			if w < prev {
				t.Errorf("watermark retreated %d -> %d", prev, w)
			}
			prev = w
		}
	})
}
