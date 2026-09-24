package check

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"ontology/pmap"
)

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}
func TestOrder(t *testing.T) {
	for _, c := range []struct{ n, w, in int }{{1, 1, 200}, {1, 4, 200}, {1, 64, 200}, {4, 1, 200}, {4, 4, 200}, {4, 64, 200}, {16, 1, 200}, {16, 4, 200}, {16, 64, 200}, {8, 4, 10000}} {
		var cur, peak atomic.Int64
		fn := func(_ context.Context, i int) (int, error) {
			if v := cur.Add(1); v > peak.Load() {
				peak.Store(v)
			}
			cur.Add(-1)
			time.Sleep(time.Duration(i*7919%47) * time.Microsecond)
			return i * 2, nil
		}
		want, _ := Naive(context.Background(), seq(c.in), fn)
		var got []int
		err := pmap.Run(context.Background(), seq(c.in), c.n, c.w, fn, func(_, out int) { got = append(got, out) })
		if err != nil || !slices.Equal(got, want) || peak.Load() > int64(c.n) || pmap.LastMaxBuffered() > c.w {
			t.Errorf("n=%d w=%d: err=%v match=%v peak=%d max=%d", c.n, c.w, err, slices.Equal(got, want), peak.Load(), pmap.LastMaxBuffered())
		}
	}
}
func TestFailDeterministic(t *testing.T) {
	err3, err5 := errors.New("e3"), errors.New("e5")
	gated := func(f5, a3 chan struct{}) func(context.Context, int) (int, error) {
		return func(_ context.Context, i int) (int, error) {
			if i == 5 {
				close(f5)
				return 0, err5
			}
			if i == 3 {
				<-a3
				return 0, err3
			}
			return i, nil
		}
	}
	base := runtime.NumGoroutine()
	for it := 0; it < 200; it++ {
		f5, a3, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		var emitted []int
		emit := func(i, _ int) { emitted = append(emitted, i) }
		go func() { done <- pmap.Run(context.Background(), seq(6), 8, 8, gated(f5, a3), emit) }()
		<-f5 // 下标 5 先失败，再放下标 3 失败
		close(a3)
		if err := <-done; !errors.Is(err, err3) || errors.Is(err, err5) || !slices.Equal(emitted, []int{0, 1, 2}) {
			t.Fatalf("it=%d: err=%v emitted=%v", it, err, emitted)
		}
	}
	f5, a3, first := make(chan struct{}), make(chan struct{}), make(chan error, 6)
	for i := range 6 { // 对照：内联「返回第一个到达的错误」的错误实现
		go func() {
			if _, err := gated(f5, a3)(context.Background(), i); err != nil {
				first <- err
			}
		}()
	}
	<-f5
	if err := <-first; !errors.Is(err, err5) {
		t.Fatalf("先到先返实现应返回 e5: %v", err)
	}
	close(a3)
	for i := 0; i < 200 && runtime.NumGoroutine() > base; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if runtime.NumGoroutine() > base {
		t.Error("goroutine 未回到基线")
	}
}
func TestErrors(t *testing.T) {
	fn := func(context.Context, int) (int, error) { return 0, nil }
	for _, c := range []struct{ n, w int }{{0, 1}, {1, 0}, {-3, -3}} {
		if err := pmap.Run(context.Background(), seq(3), c.n, c.w, fn, func(int, int) {}); !errors.Is(err, pmap.ErrBadConfig) {
			t.Errorf("n=%d w=%d: %v", c.n, c.w, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pmap.Run(ctx, seq(3), 1, 1, fn, func(int, int) {}); !errors.Is(err, context.Canceled) {
		t.Errorf("外部取消: %v", err)
	}
}
