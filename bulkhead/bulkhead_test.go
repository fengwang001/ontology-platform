package bulkhead

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/classify"
)

func mustNew(t *testing.T, cfg Config) *Bulkhead {
	t.Helper()
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestReleaseAllPaths(t *testing.T) {
	paths := map[string]func() error{
		"success": func() error { return nil },
		"failure": func() error { return errors.New("boom") },
		"timeout": func() error { return classify.ErrTimeout },
		"panic":   func() error { panic("kaboom") },
	}
	const n, rounds = 4, 1000
	for name, fn := range paths {
		b := mustNew(t, Config{Concurrency: n, Queue: 0})
		for i := 0; i < rounds; i++ {
			err := b.Execute(context.Background(), fn)
			if name == "success" && err != nil {
				t.Fatalf("%s: unexpected err %v", name, err)
			}
			if name != "success" && err == nil {
				t.Fatalf("%s: want err", name)
			}
		}
		if got := b.Available(); got != n {
			t.Errorf("%s: available = %d, want %d", name, got, n)
		}
		if got := b.InFlight(); got != 0 {
			t.Errorf("%s: inflight = %d, want 0", name, got)
		}
	}
	if err := mustNew(t, Config{Concurrency: 1}).Execute(context.Background(), paths["panic"]); !errors.Is(err, classify.ErrPanic) {
		t.Errorf("panic path: err = %v, want ErrPanic", err)
	}
}

func TestPeakConcurrency(t *testing.T) {
	cases := []struct {
		name               string
		n, queue, routines int
		wantPeak           int
	}{
		{"N=8 500协程", 8, 500, 500, 8},
		{"N=1 退化为串行", 1, 100, 100, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := mustNew(t, Config{Concurrency: tc.n, Queue: tc.queue})
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < tc.routines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_ = b.Execute(context.Background(), func() error {
						time.Sleep(2 * time.Millisecond)
						return nil
					})
				}()
			}
			close(start)
			wg.Wait()
			if b.Peak() != tc.wantPeak {
				t.Errorf("peak = %d, want %d", b.Peak(), tc.wantPeak)
			}
			if b.Peak() > tc.n {
				t.Errorf("peak %d exceeds concurrency %d", b.Peak(), tc.n)
			}
		})
	}
}

func TestQueueFullRejectsImmediately(t *testing.T) {
	fixed := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	b := mustNew(t, Config{Concurrency: 1, Queue: 2, Now: func() time.Time { return fixed }})
	hold, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 2; i++ { // 占满等待队列
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = b.Acquire(ctx) }()
	}
	for b.waitingCount() < 2 {
		time.Sleep(time.Millisecond)
	}
	_, err = b.Acquire(context.Background()) // 第 N+Q+1 个
	var rej *RejectedError
	if !errors.As(err, &rej) || !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want RejectedError", err)
	}
	if !rej.At.Equal(fixed) {
		t.Errorf("reject advanced clock to %v, want %v", rej.At, fixed)
	}
	hold()   // 放行令牌
	cancel() // 取消仍在等待的协程
	wg.Wait()
}

func TestCancelReleasesQueueSlot(t *testing.T) {
	b := mustNew(t, Config{Concurrency: 1, Queue: 1})
	hold, _ := b.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := b.Acquire(ctx); errc <- err }()
	for b.waitingCount() < 1 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled", err)
	}
	if got := b.waitingCount(); got != 0 {
		t.Fatalf("waiting = %d, want 0 (名额未归还)", got)
	}
	// 名额已归还：新请求应能入队等待而非被拒
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _, _ = b.Acquire(ctx2) }()
	for b.waitingCount() < 1 {
		time.Sleep(time.Millisecond)
	}
	hold()
}

func TestIsolationAcrossDownstreams(t *testing.T) {
	x := mustNew(t, Config{Concurrency: 2, Queue: 0})
	y := mustNew(t, Config{Concurrency: 2, Queue: 0})
	h1, _ := x.Acquire(context.Background())
	h2, _ := x.Acquire(context.Background())
	defer h1()
	defer h2()
	const calls = 100
	ok := 0
	for i := 0; i < calls; i++ {
		if y.Execute(context.Background(), func() error { return nil }) == nil {
			ok++
		}
	}
	if ok != calls {
		t.Errorf("Y success = %d/%d, X 耗尽不应影响 Y", ok, calls)
	}
	if got := x.InFlight(); got != 2 {
		t.Errorf("X inflight = %d, want 2", got)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []Config{
		{Concurrency: 0},
		{Concurrency: -1},
		{Concurrency: 1, Queue: -1},
	}
	for _, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v): want error", cfg)
		}
	}
}

func (b *Bulkhead) waitingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waiting
}

func TestSentinelErrorsIs(t *testing.T) {
	sentinels := []error{breaker.ErrOpen, breaker.ErrClockRollback, ErrRejected,
		classify.ErrTimeout, classify.ErrPanic, classify.ErrNonRetryable}
	for i, s := range sentinels {
		w := fmt.Errorf("ctx: %w", s)
		if !errors.Is(w, s) {
			t.Errorf("errors.Is(wrapped %v) = false", s)
		}
		for j, o := range sentinels {
			if i != j && errors.Is(w, o) {
				t.Errorf("%v unexpectedly matches %v", s, o)
			}
		}
	}
}
