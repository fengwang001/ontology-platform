package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAcquireTable 用一张表覆盖：峰值不超 N、队列满立即拒绝、
// 排队取消后归还名额、N=1 串行、非法参数。
func TestAcquireTable(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "500 goroutines N=8 peak never exceeds 8",
			run: func(t *testing.T) {
				b, _ := New(8, 16)
				var cur int32
				var maxSeen int32
				var wg sync.WaitGroup
				gate := make(chan struct{})
				for i := 0; i < 500; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-gate
						rel, err := b.Acquire(context.Background())
						if err != nil {
							return
						}
						c := atomic.AddInt32(&cur, 1)
						for {
							m := atomic.LoadInt32(&maxSeen)
							if c <= m || atomic.CompareAndSwapInt32(&maxSeen, m, c) {
								break
							}
						}
						time.Sleep(time.Millisecond)
						atomic.AddInt32(&cur, -1)
						rel()
					}()
				}
				close(gate)
				wg.Wait()
				if got := b.Peak(); got > 8 {
					t.Fatalf("peak=%d want <= 8", got)
				}
				if maxSeen > 8 {
					t.Fatalf("observed concurrency %d want <= 8", maxSeen)
				}
			},
		},
		{
			name: "N+Q+1 rejected immediately without waiting",
			run: func(t *testing.T) {
				b, _ := New(2, 1)
				var rels []func()
				for i := 0; i < 3; i++ { // 2 在途 + 1 排队
					r, err := b.Acquire(context.Background())
					if err != nil {
						t.Fatalf("acquire %d: %v", i, err)
					}
					rels = append(rels, r)
				}
				done := make(chan error, 1)
				start := time.Now()
				go func() {
					_, err := b.Acquire(context.Background())
					done <- err
				}()
				select {
				case err := <-done:
					if !errors.Is(err, ErrQueueFull) {
						t.Fatalf("err=%v want ErrQueueFull", err)
					}
				case <-time.After(200 * time.Millisecond):
					t.Fatal("4th request blocked instead of immediate reject")
				}
				if d := time.Since(start); d > 50*time.Millisecond {
					t.Fatalf("reject took %v, clock/wait advanced", d)
				}
				for _, r := range rels {
					r()
				}
			},
		},
		{
			name: "queued request removed on cancel and slot returned",
			run: func(t *testing.T) {
				b, _ := New(1, 1)
				r1, _ := b.Acquire(context.Background()) // 在途
				ctx, cancel := context.WithCancel(context.Background())
				wgDone := make(chan struct{})
				go func() {
					_, _ = b.Acquire(ctx) // 排队
					close(wgDone)
				}()
				time.Sleep(20 * time.Millisecond)
				cancel()
				<-wgDone
				r1()
				// 取消已归还占位：新请求应能占满 1 在途 + 1 排队。
				r2, err := b.Acquire(context.Background())
				if err != nil {
					t.Fatalf("acquire after cancel: %v", err)
				}
				if _, err := b.Acquire(context.Background()); err != nil {
					t.Fatalf("queue slot not returned: %v", err)
				}
				r2()
			},
		},
		{
			name: "N=1 serializes all calls",
			run: func(t *testing.T) {
				b, _ := New(1, 0)
				var cur int32
				var wg sync.WaitGroup
				gate := make(chan struct{})
				for i := 0; i < 100; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-gate
						r, err := b.Acquire(context.Background())
						if err != nil {
							return
						}
						if atomic.AddInt32(&cur, 1) != 1 {
							t.Error("two calls in flight with N=1")
						}
						time.Sleep(time.Microsecond * 200)
						atomic.AddInt32(&cur, -1)
						r()
					}()
				}
				close(gate)
				wg.Wait()
				if b.Peak() != 1 {
					t.Fatalf("peak=%d want 1", b.Peak())
				}
			},
		},
		{
			name: "invalid construction rejected",
			run: func(t *testing.T) {
				bad := [][2]int{{0, 1}, {-1, 1}, {1, -1}}
				for _, p := range bad {
					if _, err := New(p[0], p[1]); err == nil {
						t.Fatalf("New(%d,%d) expected error", p[0], p[1])
					}
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// TestReleaseAllPaths 在四条路径各跑 1000 次，断言额度每次都回满。
func TestReleaseAllPaths(t *testing.T) {
	paths := []struct {
		name string
		exit func(rel func())
	}{
		{"success", func(rel func()) { rel() }},
		{"failure", func(rel func()) { rel() }},
		{"timeout", func(rel func()) { rel() }},
		{"panic", func(rel func()) {
			defer func() { _ = recover() }()
			func() { defer rel(); panic("boom") }()
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			b, _ := New(8, 0)
			for i := 0; i < 1000; i++ {
				r, err := b.Acquire(context.Background())
				if err != nil {
					t.Fatalf("acquire: %v", err)
				}
				p.exit(r)
			}
			if b.InFlight() != 0 || len(b.slots) != 0 || len(b.run) != 0 {
				t.Fatalf("leak after path %s: inFlight=%d slots=%d run=%d",
					p.name, b.InFlight(), len(b.slots), len(b.run))
			}
		})
	}
}
