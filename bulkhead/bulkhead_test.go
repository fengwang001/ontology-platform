package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func TestBulkheadTable(t *testing.T) {
	type tc struct {
		name string
		run  func(t *testing.T)
	}
	cases := []tc{
		{"invalid config rejected", func(t *testing.T) {
			bad := [][2]int{{0, 1}, {-1, 1}, {1, 0}, {2, -3}}
			for _, c := range bad {
				if _, err := New(c[0], c[1]); !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("New(%d,%d)=%v", c[0], c[1], err)
				}
			}
		}},
		{"queue full rejects immediately without clock advance", func(t *testing.T) {
			b, _ := New(1, 1)
			block := make(chan struct{})
			got1 := make(chan error, 1)
			go func() {
				_ = b.Acquire(context.Background())
				got1 <- nil
				<-block
				b.Release()
			}()
			time.Sleep(5 * time.Millisecond)
			<-got1
			gotQ := make(chan error, 1)
			go func() { gotQ <- b.Acquire(context.Background()) }()
			time.Sleep(5 * time.Millisecond)
			clk := &fakeClock{t: time.Unix(1000, 0)}
			start := clk.t
			err := b.Acquire(context.Background())
			if !errors.Is(err, ErrBulkheadRejected) {
				t.Fatalf("third acquire = %v", err)
			}
			if !clk.t.Equal(start) {
				t.Fatalf("clock advanced on reject")
			}
			close(block)
			if e := <-gotQ; e != nil {
				t.Fatalf("queued acquire = %v", e)
			}
			b.Release()
		}},
		{"cancel removes waiter and restores capacity", func(t *testing.T) {
			b, _ := New(1, 2)
			hold := make(chan struct{})
			if err := b.Acquire(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			werr := make(chan error, 1)
			go func() { werr <- b.Acquire(ctx) }()
			time.Sleep(5 * time.Millisecond)
			if b.Waiting() != 1 || b.Available() != 0 {
				t.Fatalf("waiting=%d avail=%d", b.Waiting(), b.Available())
			}
			cancel()
			if e := <-werr; !errors.Is(e, context.Canceled) {
				t.Fatalf("waiter err=%v", e)
			}
			time.Sleep(5 * time.Millisecond)
			if b.Waiting() != 0 {
				t.Fatalf("waiter not removed: %d", b.Waiting())
			}
			close(hold)
			b.Release()
			if b.Available() != 1 {
				t.Fatalf("available=%d after cancel", b.Available())
			}
		}},
		{"capacity one serializes", func(t *testing.T) {
			b, _ := New(1, 1)
			for i := 0; i < 100; i++ {
				if err := b.Acquire(context.Background()); err != nil {
					t.Fatal(err)
				}
				if b.Running() != 1 {
					t.Fatal("running != 1")
				}
				b.Release()
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

func TestBulkheadConcurrency(t *testing.T) {
	// 500 协程、N=8：峰值恒不超过 8；四条路径各 1000 次后额度回满。
	b, _ := New(8, 16)
	var inFlight, peak int32
	var wg sync.WaitGroup
	paths := []func(){
		func() {},
		func() { panic("x") },
	}
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := b.Acquire(context.Background()); err != nil {
				return
			}
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			if i%2 == 0 && len(paths) > 1 {
				func() {
					defer func() { _ = recover() }()
					panic("boom")
				}()
			}
			atomic.AddInt32(&inFlight, -1)
			b.Release()
		}(i)
	}
	wg.Wait()
	if peak > 8 {
		t.Fatalf("peak=%d > 8", peak)
	}
	if got := b.Peak(); got > 8 {
		t.Fatalf("recorded peak=%d", got)
	}

	// 成功/失败/超时/panic 四条路径各 1000 次，额度必须回到满值。
	for round := 0; round < 4; round++ {
		for i := 0; i < 1000; i++ {
			ctx := context.Background()
			if err := b.Acquire(ctx); err != nil {
				t.Fatalf("acquire round=%d: %v", round, err)
			}
			switch round {
			case 0:
			case 1:
				_ = errors.New("failure")
			case 2:
				ctx2, c := context.WithTimeout(ctx, time.Nanosecond)
				<-ctx2.Done()
				c()
			case 3:
				func() { defer func() { _ = recover() }(); panic("p") }()
			}
			b.Release()
		}
	}
	if b.Available() != 8 {
		t.Fatalf("available=%d, want 8", b.Available())
	}

	// 下游 X 耗尽额度时，下游 Y 不受影响。
	x, _ := New(1, 1)
	y, _ := New(4, 4)
	_ = x.Acquire(context.Background())
	ok := 0
	for i := 0; i < 100; i++ {
		if err := y.Acquire(context.Background()); err == nil {
			ok++
			y.Release()
		}
	}
	if ok != 100 {
		t.Fatalf("Y success=%d, want 100", ok)
	}
	x.Release()
}
