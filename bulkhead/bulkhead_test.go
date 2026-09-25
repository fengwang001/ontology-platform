package bulkhead

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewValidation(t *testing.T) {
	cases := []struct {
		n, q  int
		valid bool
	}{
		{1, 0, true}, {8, 16, true}, {0, 1, false}, {-1, 1, false}, {1, -1, false},
	}
	for _, c := range cases {
		_, err := New(c.n, c.q)
		if got := err == nil; got != c.valid {
			t.Errorf("New(%d,%d) valid=%v want %v (err=%v)", c.n, c.q, got, c.valid, err)
		}
	}
}

func TestPeakNeverExceedsN(t *testing.T) {
	for _, n := range []int{1, 8} {
		b, _ := New(n, 600)
		var wg sync.WaitGroup
		for i := 0; i < 500; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rel, err := b.Acquire(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				time.Sleep(time.Millisecond)
				rel()
			}()
		}
		wg.Wait()
		if b.Peak() > n {
			t.Errorf("N=%d peak=%d exceeds N", n, b.Peak())
		}
		if b.Available() != n {
			t.Errorf("N=%d available=%d want %d", n, b.Available(), n)
		}
	}
}

func TestQueueFullRejectsImmediately(t *testing.T) {
	b, _ := New(1, 1)
	rel, _ := b.Acquire(context.Background())
	waiting := make(chan error, 1)
	go func() {
		_, err := b.Acquire(context.Background())
		waiting <- err
	}()
	time.Sleep(20 * time.Millisecond) // 让等待者进入队列
	start := time.Now()
	_, err := b.Acquire(context.Background())
	if !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("err=%v want ErrBulkheadFull", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("reject took %v, not immediate", d)
	}
	rel() // 释放后等待者应拿到额度
	if err := <-waiting; err != nil {
		t.Fatalf("waiter err=%v", err)
	}
}

func TestCancelWhileWaiting(t *testing.T) {
	b, _ := New(1, 1)
	rel, _ := b.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := b.Acquire(ctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
	rel()
	// 取消后名额已归还：新请求应能立即拿到额度
	rel2, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("after cancel: %v", err)
	}
	rel2()
	if b.Available() != 1 {
		t.Fatalf("available=%d want 1", b.Available())
	}
}

func TestReleaseOnAllPaths(t *testing.T) {
	calls := []struct {
		name string
		fn   func() error
	}{
		{"success", func() error { return nil }},
		{"failure", func() error { return errors.New("boom") }},
		{"timeout", func() error { time.Sleep(2 * time.Millisecond); return nil }},
		{"panic", func() error { panic("boom") }},
	}
	for _, c := range calls {
		b, _ := New(4, 0)
		for i := 0; i < 1000; i++ {
			func() {
				rel, err := b.Acquire(context.Background())
				if err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				defer rel()
				defer func() { _ = recover() }()
				_ = c.fn()
			}()
		}
		if b.Available() != 4 {
			t.Errorf("%s: available=%d want 4 (leak)", c.name, b.Available())
		}
	}
}

func TestIsolationBetweenDownstreams(t *testing.T) {
	x, _ := New(2, 0)
	y, _ := New(2, 0)
	r1, _ := x.Acquire(context.Background())
	r2, _ := x.Acquire(context.Background())
	defer r1()
	defer r2()
	if _, err := x.Acquire(context.Background()); !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("X should be exhausted, err=%v", err)
	}
	ok := 0
	for i := 0; i < 100; i++ {
		rel, err := y.Acquire(context.Background())
		if err == nil {
			ok++
			rel()
		}
	}
	if ok != 100 {
		t.Fatalf("Y succeeded %d/100 while X exhausted", ok)
	}
}
