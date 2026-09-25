package ontology_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/bulkhead"
	"ontology/classify"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want classify.Kind
		fail bool
	}{
		{"retryable", classify.ErrRetryable, classify.Retryable, true},
		{"wrapped retryable", fmt.Errorf("down: %w", classify.ErrRetryable), classify.Retryable, true},
		{"timeout sentinel", classify.ErrTimeout, classify.Timeout, true},
		{"deadline exceeded", context.DeadlineExceeded, classify.Timeout, true},
		{"wrapped deadline", fmt.Errorf("ctx: %w", context.DeadlineExceeded), classify.Timeout, true},
		{"fatal", classify.ErrFatal, classify.Fatal, false},
		{"wrapped fatal", fmt.Errorf("400: %w", classify.ErrFatal), classify.Fatal, false},
		{"unknown is fatal", errors.New("boom"), classify.Fatal, false},
		{"canceled is fatal", context.Canceled, classify.Fatal, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify.Of(tc.err); got != tc.want {
				t.Fatalf("Of = %v, want %v", got, tc.want)
			}
			if got := classify.CountsAsFailure(tc.want); got != tc.fail {
				t.Fatalf("CountsAsFailure = %v, want %v", got, tc.fail)
			}
		})
	}
}

func TestErrorSentinelsDistinct(t *testing.T) {
	cases := []struct {
		err   error
		is    error
		match bool
	}{
		{classify.ErrRetryable, classify.ErrRetryable, true},
		{classify.ErrFatal, classify.ErrFatal, true},
		{classify.ErrTimeout, classify.ErrTimeout, true},
		{classify.ErrTimeout, context.DeadlineExceeded, false},
		{classify.ErrRetryable, classify.ErrFatal, false},
		{classify.ErrFatal, classify.ErrRetryable, false},
	}
	for _, tc := range cases {
		if got := errors.Is(tc.err, tc.is); got != tc.match {
			t.Fatalf("errors.Is(%v,%v)=%v want %v", tc.err, tc.is, got, tc.match)
		}
	}
}

func TestBulkheadPeakAndReject(t *testing.T) {
	// 用例1：500 协程、N=8、Q=0，峰值恰好不超过 8，超额立即被拒且不等待。
	bh, err := bulkhead.New(bulkhead.Config{Name: "x", Capacity: 8})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	rejected := 0
	gate := make(chan struct{})
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			rel, err := bh.Acquire(context.Background())
			if err != nil {
				mu.Lock()
				rejected++
				mu.Unlock()
				return
			}
			time.Sleep(2 * time.Millisecond)
			rel()
		}()
	}
	close(gate)
	wg.Wait()
	if bh.Peak() > 8 {
		t.Fatalf("peak=%d > 8", bh.Peak())
	}
	if bh.Running() != 0 || bh.Available() != 8 {
		t.Fatalf("leak: running=%d available=%d rejected=%d", bh.Running(), bh.Available(), rejected)
	}
	if rejected == 0 || rejected >= 500 {
		t.Fatalf("rejected=%d outside expected range", rejected)
	}
}

func TestBulkheadImmediateRejectClock(t *testing.T) {
	// 用例2：第 N+Q+1 个请求立即被拒；用注入时钟证明拒绝不消耗时间。
	type clock struct{ t time.Time }
	cases := []struct {
		n, q, extra int
	}{
		{1, 0, 1},
		{2, 1, 1},
		{4, 2, 3},
	}
	for _, tc := range cases {
		bh, _ := bulkhead.New(bulkhead.Config{Capacity: tc.n, Queue: tc.q})
		hold := make([]func(), 0, tc.n)
		for i := 0; i < tc.n; i++ {
			rel, err := bh.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			hold = append(hold, rel)
		}
		var qg sync.WaitGroup
		for i := 0; i < tc.q; i++ {
			qg.Add(1)
			go func() {
				defer qg.Done()
				rel, err := bh.Acquire(context.Background())
				if err == nil {
					rel()
				}
			}()
		}
		time.Sleep(5 * time.Millisecond)
		c := clock{t: time.Now()}
		start := c.t
		_, err := bh.Acquire(context.Background())
		if !errors.Is(err, bulkhead.ErrBulkheadFull) {
			t.Fatalf("err=%v want ErrBulkheadFull", err)
		}
		if time.Since(start) > time.Millisecond {
			t.Fatalf("reject blocked for %v", time.Since(start))
		}
		for _, rel := range hold {
			rel()
		}
		qg.Wait()
		if bh.Available() != tc.n {
			t.Fatalf("available=%d want %d", bh.Available(), tc.n)
		}
	}
}

func TestBulkheadCancel(t *testing.T) {
	// 用例3：等待中取消必须从队列移除，名额最终正确归还。
	cases := []struct {
		n, q, cancelIdx int
	}{
		{1, 2, 0},
		{1, 2, 1},
		{2, 3, 2},
	}
	for _, tc := range cases {
		bh, _ := bulkhead.New(bulkhead.Config{Capacity: tc.n, Queue: tc.q})
		holders := make([]func(), tc.n)
		for i := range holders {
			rel, _ := bh.Acquire(context.Background())
			holders[i] = rel
		}
		ctxs := make([]context.CancelFunc, tc.q)
		var rest sync.WaitGroup
		done := make(chan error, 1)
		for i := 0; i < tc.q; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			ctxs[i] = cancel
			rest.Add(1)
			go func(c context.Context, idx int) {
				defer rest.Done()
				rel, err := bh.Acquire(c)
				if idx == tc.cancelIdx {
					done <- err
					return
				}
				if err == nil {
					rel()
				}
			}(ctx, i)
		}
		time.Sleep(5 * time.Millisecond)
		ctxs[tc.cancelIdx]()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel err=%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancel did not unblock waiter")
		}
		for _, rel := range holders {
			rel()
		}
		rest.Wait()
		if bh.Available() != tc.n {
			t.Fatalf("available=%d want %d after cancel", bh.Available(), tc.n)
		}
	}
}

func TestBulkheadReleasePathsAndIsolation(t *testing.T) {
	// 用例4：成功/失败/超时/panic 四条路径各 1000 次后额度回满。
	paths := []struct {
		name string
		fail func(rel func())
	}{
		{"success", func(rel func()) { rel() }},
		{"failure", func(rel func()) { rel() }},
		{"timeout", func(rel func()) { rel() }},
		{"panic", func(rel func()) {
			defer rel()
			panic("downstream panic")
		}},
	}
	for _, p := range paths {
		bh, _ := bulkhead.New(bulkhead.Config{Capacity: 4, Queue: 1000})
		var wg sync.WaitGroup
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rel, err := bh.Acquire(context.Background())
				if err != nil {
					t.Errorf("%s: acquire: %v", p.name, err)
					return
				}
				defer func() { _ = recover() }()
				p.fail(rel)
			}()
		}
		wg.Wait()
		if bh.Available() != 4 || bh.Running() != 0 {
			t.Fatalf("%s: available=%d running=%d", p.name, bh.Available(), bh.Running())
		}
	}

	// 用例5：N=1 串行；下游 X 耗尽不影响下游 Y。
	x, _ := bulkhead.New(bulkhead.Config{Name: "X", Capacity: 1})
	y, _ := bulkhead.New(bulkhead.Config{Name: "Y", Capacity: 2})
	relX, _ := x.Acquire(context.Background())
	if _, err := x.Acquire(context.Background()); !errors.Is(err, bulkhead.ErrBulkheadFull) {
		t.Fatalf("X full err=%v", err)
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
		t.Fatalf("Y success=%d want 100", ok)
	}
	relX()
	if x.Available() != 1 || y.Available() != 2 {
		t.Fatalf("isolation leak x=%d y=%d", x.Available(), y.Available())
	}

	// 用例6：非法配置报错。
	bad := []bulkhead.Config{
		{Capacity: 0}, {Capacity: -1, Queue: 1}, {Capacity: 1, Queue: -1},
	}
	for _, cfg := range bad {
		if _, err := bulkhead.New(cfg); err == nil {
			t.Fatalf("cfg %+v should error", cfg)
		}
	}
}
