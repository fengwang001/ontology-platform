package bulkhead_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/bulkhead"
	"ontology/classify"
)

func TestConfigInvalid(t *testing.T) {
	cases := []struct {
		n, q int
	}{{0, 1}, {-1, 1}, {1, -1}}
	for _, c := range cases {
		if _, err := bulkhead.New(c.n, c.q); err == nil {
			t.Fatalf("New(%d,%d) 期望报错", c.n, c.q)
		}
	}
}

func TestPeakAndSerial(t *testing.T) {
	cases := []struct {
		name     string
		n, q     int
		gor      int
		wantPeak int
	}{
		{"500协程N=8", 8, 500, 500, 8},
		{"N=1串行", 1, 10, 100, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bh, _ := bulkhead.New(c.n, c.q)
			var cur, max int32
			var wg sync.WaitGroup
			gate := make(chan struct{})
			for i := 0; i < c.gor; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-gate
					rel, err := bh.Acquire(context.Background())
					if err != nil {
						return
					}
					v := atomic.AddInt32(&cur, 1)
					for {
						old := atomic.LoadInt32(&max)
						if v <= old || atomic.CompareAndSwapInt32(&max, old, v) {
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
			if bh.Peak() != c.wantPeak || int(max) != c.wantPeak {
				t.Fatalf("peak=%d max=%d 期望 %d", bh.Peak(), max, c.wantPeak)
			}
			if bh.Available() != c.n {
				t.Fatalf("available=%d 期望 %d", bh.Available(), c.n)
			}
		})
	}
}

func TestQueueFullImmediateAndClockNotAdvanced(t *testing.T) {
	bh, _ := bulkhead.New(1, 1)
	rel1, err := bh.Acquire(context.Background()) // 占满在途
	if err != nil {
		t.Fatal(err)
	}
	gotSlot := make(chan struct{})
	go func() {
		rel2, err := bh.Acquire(context.Background()) // 占满队列
		if err != nil {
			t.Error(err)
			return
		}
		close(gotSlot)
		rel2()
	}()
	time.Sleep(5 * time.Millisecond)
	clk := classify.NewFakeClock(time.Unix(0, 0))
	before := clk.Now()
	_, err = bh.Acquire(context.Background()) // N+Q+1：必须立即拒绝
	after := clk.Now()
	if !errors.Is(err, classify.ErrBulkheadRejected) {
		t.Fatalf("err=%v 期望 ErrBulkheadRejected", err)
	}
	if !after.Equal(before) {
		t.Fatalf("拒绝推进了时钟: %v -> %v", before, after)
	}
	rel1()
	<-gotSlot
	if bh.Available() != 1 {
		t.Fatalf("available=%d 期望 1", bh.Available())
	}
}

func TestCancelWaitedRemoved(t *testing.T) {
	cases := []struct {
		name  string
		n, q  int
		enq   int
		kill  int // 取消第几个等待者
	}{
		{"队首取消", 1, 4, 4, 0},
		{"队中非首取消", 1, 4, 4, 2},
		{"队尾取消", 1, 4, 4, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bh, _ := bulkhead.New(c.n, c.q)
			rels := make([]func(), c.enq+1)
			var rmu sync.Mutex
			rels[0], _ = bh.Acquire(context.Background())
			ctxs := make([]context.CancelFunc, c.enq)
			done := make(chan error, c.enq)
			for i := 0; i < c.enq; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				ctxs[i] = cancel
				go func(ctx context.Context) {
					rel, err := bh.Acquire(ctx)
					if err == nil {
						rmu.Lock()
						rels = append(rels, rel)
						rmu.Unlock()
					}
					done <- err
				}(ctx)
			}
			time.Sleep(5 * time.Millisecond)
			ctxs[c.kill]()
			time.Sleep(5 * time.Millisecond)
			if bh.Available() != 0 {
				t.Fatalf("取消等待者不应腾出在途名额, available=%d", bh.Available())
			}
			for i := 0; i < c.enq; i++ {
				ctxs[i]()
			}
			rels[0]()
			errs := 0
			for i := 0; i < c.enq; i++ {
				if <-done != nil {
					errs++
				}
			}
			if errs != c.enq {
				t.Fatalf("全部取消后 %d/%d 返回错误", errs, c.enq)
			}
			rmu.Lock()
			for _, rel := range rels[1:] {
				if rel != nil {
					rel()
				}
			}
			rmu.Unlock()
			if bh.Available() != c.n {
				t.Fatalf("available=%d 期望 %d", bh.Available(), c.n)
			}
		})
	}
}
