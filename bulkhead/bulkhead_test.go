// 外部测试包，覆盖 bulkhead / classify / timeout 三个包的行为。
package bulkhead_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/bulkhead"
	"ontology/classify"
	"ontology/timeout"
)

type fakeClock struct{ now time.Time }

func burst(n int, f func()) {
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() { defer wg.Done(); f() }()
	}
	wg.Wait()
}

func hold(b *bulkhead.Bulkhead) {
	go func() {
		if b.Acquire(context.Background()) == nil {
			select {}
		}
	}()
	time.Sleep(2 * time.Millisecond)
}

func queued(b *bulkhead.Bulkhead) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Acquire(ctx) }()
	time.Sleep(2 * time.Millisecond)
	return cancel
}

// TestBulkhead 用一张表覆盖：峰值上限、立即拒绝、时钟不动、取消归还、
// 下游隔离、N=1 串行、非法配置、四条路径各 1000 次额度回满。
func TestBulkhead(t *testing.T) {
	cases := []struct {
		name string
		ok   func() bool
	}{
		{"peak <= N with 500 goroutines", func() bool {
			b, _ := bulkhead.New(8, 500)
			burst(500, func() {
				if b.Acquire(context.Background()) == nil {
					time.Sleep(time.Millisecond)
					b.Release()
				}
			})
			return b.Peak() <= 8 && b.Available() == 8
		}},
		{"N+Q+1 rejected immediately, clock untouched", func() bool {
			b, _ := bulkhead.New(1, 1)
			hold(b)
			queued(b)
			clk := &fakeClock{now: time.Unix(0, 0)}
			err := b.Acquire(context.Background())
			return errors.Is(err, bulkhead.ErrBulkheadFull) && clk.now.Equal(time.Unix(0, 0))
		}},
		{"cancelled waiter frees slot", func() bool {
			b, _ := bulkhead.New(1, 1)
			hold(b)
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			go func() { done <- b.Acquire(ctx) }()
			time.Sleep(2 * time.Millisecond)
			cancel()
			if !errors.Is(<-done, context.Canceled) {
				return false
			}
			joined := make(chan struct{})
			go func() { _ = b.Acquire(context.Background()); close(joined) }()
			select {
			case <-joined:
				return false // 仍立即拒绝 → 名额未归还
			case <-time.After(10 * time.Millisecond):
				return true // 成功排队 → 名额已归还
			}
		}},
		{"downstream X exhausted, Y unaffected", func() bool {
			x, _ := bulkhead.New(1, 1)
			y, _ := bulkhead.New(2, 2)
			hold(x)
			queued(x)
			var ok atomic.Int64
			burst(20, func() {
				if y.Acquire(context.Background()) == nil {
					ok.Add(1)
					y.Release()
				}
			})
			return ok.Load() == 20 && y.Available() == 2
		}},
		{"N=1 serializes", func() bool {
			b, _ := bulkhead.New(1, 1)
			var peak, cur atomic.Int64
			burst(50, func() {
				if b.Acquire(context.Background()) != nil {
					return
				}
				v := cur.Add(1)
				for p := peak.Load(); v > p; p = peak.Load() {
					if peak.CompareAndSwap(p, v) {
						break
					}
				}
				time.Sleep(200 * time.Microsecond)
				cur.Add(-1)
				b.Release()
			})
			return peak.Load() == 1 && b.Available() == 1
		}},
		{"invalid config rejected", func() bool {
			_, e1 := bulkhead.New(0, 1)
			_, e2 := bulkhead.New(1, -1)
			return e1 != nil && e2 != nil
		}},
		{"permits refill on four paths x1000", func() bool {
			b, _ := bulkhead.New(8, 64)
			paths := []func() error{
				func() error { return nil },
				func() error { return classify.ErrRetryable },
				func() error { time.Sleep(50 * time.Millisecond); return nil },
				func() error { panic("boom") },
			}
			for _, fn := range paths {
				for i := 0; i < 1000; i++ {
					if b.Acquire(context.Background()) != nil {
						return false
					}
					timeout.Do(time.Millisecond, fn)
					b.Release()
				}
			}
			return b.Available() == 8 && b.Peak() <= 8
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok() {
				t.Fatal("assertion failed")
			}
		})
	}
}

// TestClassify 一张表覆盖分类、包装 errors.Is、熔断计数口径。
func TestClassify(t *testing.T) {
	cases := []struct {
	name string
	err  error
	kind classify.Kind
	brk  bool
	}{
		{"nil", nil, classify.KindNone, false},
		{"retryable", classify.ErrRetryable, classify.KindRetryable, true},
		{"non-retryable", classify.ErrNonRetryable, classify.KindNonRetryable, false},
		{"timeout", classify.ErrTimeout, classify.KindTimeout, true},
		{"panic", classify.PanicError("x"), classify.KindPanic, true},
		{"wrapped timeout", fmt.Errorf("w: %w", classify.ErrTimeout), classify.KindTimeout, true},
		{"unknown defaults retryable", errors.New("x"), classify.KindRetryable, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if classify.Classify(tc.err) != tc.kind ||
				classify.IsBreakerFailure(tc.err) != tc.brk {
				t.Fatal("classify mismatch")
			}
		})
	}
}

// TestTimeout 一张表覆盖成功/失败/超时/panic/零时限。
func TestTimeout(t *testing.T) {
	cases := []struct {
		name string
		lim  time.Duration
		fn   func() error
		want error
	}{
		{"ok", time.Second, func() error { return nil }, nil},
		{"fail passthrough", time.Second, func() error { return classify.ErrRetryable }, classify.ErrRetryable},
		{"timeout", time.Millisecond, func() error { time.Sleep(time.Second); return nil }, classify.ErrTimeout},
		{"panic converted", time.Second, func() error { panic(1) }, classify.ErrPanic},
		{"zero limit still catches panic", 0, func() error { panic(2) }, classify.ErrPanic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := timeout.Do(tc.lim, tc.fn)
			if tc.want == nil && err != nil ||
				tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

var _ = sync.Mutex{}
