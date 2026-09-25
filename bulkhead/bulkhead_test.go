package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/timeout"
)

type mclock struct{ t time.Time }

func (m *mclock) Now() time.Time { return m.t }

func TestNewValidation(t *testing.T) {
	cases := []struct {
		n, q   int
		wantEr bool
	}{
		{0, 1, true}, {-1, 0, true}, {1, -1, true}, {1, 0, false}, {8, 16, false},
	}
	for _, tc := range cases {
		_, err := New(tc.n, tc.q)
		if (err != nil) != tc.wantEr {
			t.Errorf("New(%d,%d) err=%v, wantErr=%v", tc.n, tc.q, err, tc.wantEr)
		}
	}
}

func TestPeakConcurrency(t *testing.T) {
	b, _ := New(8, 500)
	entered := make(chan struct{}, 500)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Execute(context.Background(), func(context.Context) error {
				entered <- struct{}{}
				<-gate
				return nil
			})
		}()
	}
	for i := 0; i < 8; i++ {
		<-entered
	}
	close(gate)
	wg.Wait()
	if got := b.peakInFlight(); got > 8 {
		t.Errorf("峰值 %d 超过额度 8", got)
	} else if got != 8 {
		t.Errorf("峰值 %d, 预期恰好达到 8", got)
	}
}

func TestQueueFullRejectedImmediately(t *testing.T) {
	b, _ := New(1, 1)
	ctx := context.Background()
	if err := b.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	waiter := make(chan error, 1)
	go func() { waiter <- b.Acquire(ctx) }()
	time.Sleep(20 * time.Millisecond) // 等 waiter 进入队列
	mc := &mclock{t: time.Now()}
	before := mc.Now()
	done := make(chan error, 1)
	go func() { done <- b.Acquire(ctx) }() // 第 N+Q+1 个请求
	select {
	case err := <-done:
		if !errors.Is(err, ErrFull) {
			t.Errorf("got %v, want ErrFull", err)
		}
	case <-time.After(time.Second):
		t.Fatal("队列满时未立即拒绝")
	}
	if !mc.Now().Equal(before) {
		t.Error("拒绝发生时注入时钟被推进")
	}
	b.Release()
	if err := <-waiter; err != nil {
		t.Fatal(err)
	}
	b.Release()
}

func TestCancelWhileQueued(t *testing.T) {
	b, _ := New(1, 1)
	ctx := context.Background()
	b.Acquire(ctx)
	ctx2, cancel := context.WithCancel(ctx)
	res := make(chan error, 1)
	go func() { res <- b.Acquire(ctx2) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的等待者: got %v, want context.Canceled", err)
	}
	// 队列名额已归还：新等待者应能进队列而不是立即 ErrFull
	ctx3, cancel3 := context.WithCancel(ctx)
	res2 := make(chan error, 1)
	go func() { res2 <- b.Acquire(ctx3) }()
	select {
	case err := <-res2:
		if errors.Is(err, ErrFull) {
			t.Error("取消后队列名额未归还")
		}
	case <-time.After(50 * time.Millisecond):
	}
	cancel3()
	<-res2
	b.Release()
	if got := b.Available(); got != 1 {
		t.Errorf("结束后可用额度 = %d, 应为 1", got)
	}
}

func TestPermitReturnedFourPaths(t *testing.T) {
	paths := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"success", func(context.Context) error { return nil }},
		{"failure", func(context.Context) error { return errors.New("boom") }},
		{"timeout", func(ctx context.Context) error {
			return timeout.Do(ctx, time.Millisecond, func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			})
		}},
		{"panic", func(context.Context) error { panic("bang") }},
	}
	for _, p := range paths {
		b, _ := New(8, 0)
		for i := 0; i < 1000; i++ {
			b.Execute(context.Background(), p.fn)
		}
		if got := b.Available(); got != 8 {
			t.Errorf("%s: 1000 次后可用额度 = %d, 应为满值 8", p.name, got)
		}
	}
}

func TestSerialWhenN1(t *testing.T) {
	b, _ := New(1, 100)
	var cur, max atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Execute(context.Background(), func(ctx context.Context) error {
				c := cur.Add(1)
				for {
					if m := max.Load(); c <= m || max.CompareAndSwap(m, c) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				cur.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()
	if got := max.Load(); got != 1 {
		t.Errorf("N=1 时最大并发 = %d, 应为 1", got)
	}
}

func TestIsolationAcrossDownstreams(t *testing.T) {
	x, _ := New(2, 0)
	y, _ := New(2, 0)
	gate := make(chan struct{})
	for i := 0; i < 2; i++ {
		go x.Execute(context.Background(), func(context.Context) error { <-gate; return nil })
	}
	for deadline := time.Now().Add(time.Second); x.Available() != 0; {
		if time.Now().After(deadline) {
			t.Fatal("X 额度未被占满")
		}
		time.Sleep(time.Millisecond)
	}
	ok := 0
	for i := 0; i < 100; i++ {
		if y.Execute(context.Background(), func(context.Context) error { return nil }) == nil {
			ok++
		}
	}
	if err := x.Acquire(context.Background()); !errors.Is(err, ErrFull) {
		t.Error("X 额度耗尽后未拒绝")
	}
	close(gate)
	if ok != 100 {
		t.Errorf("X 耗尽时 Y 成功 %d/100, 应为 100", ok)
	}
}
