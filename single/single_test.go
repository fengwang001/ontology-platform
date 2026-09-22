package single

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
)

func TestSameKeyMerges(t *testing.T) {
	g := New[int](0)
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func(ctx context.Context) (int, error) {
		close(started)
		<-release
		return 42, nil
	}
	const waiters = 8
	results := make([]int, waiters)
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := g.Do(context.Background(), "k", fn)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = v
		}(i)
	}
	<-started
	// 等其余 7 个调用全部合并进同一次执行后再放行。
	for g.Waiters("k") < waiters-1 {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	for i, v := range results {
		if v != 42 {
			t.Fatalf("waiter %d got %d want 42", i, v)
		}
	}
	if got := g.Calls(); got != 1 {
		t.Fatalf("fn called %d times, want 1", got)
	}
}

func TestDifferentKeysConcurrent(t *testing.T) {
	g := New[int](0)
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	fastDone := make(chan struct{})
	go func() {
		g.Do(context.Background(), "slow", func(ctx context.Context) (int, error) {
			close(slowStarted)
			<-slowRelease
			return 1, nil
		})
	}()
	<-slowStarted
	go func() {
		defer close(fastDone)
		v, err := g.Do(context.Background(), "fast", func(ctx context.Context) (int, error) {
			return 2, nil
		})
		if err != nil || v != 2 {
			t.Errorf("fast key got %v, %v", v, err)
		}
	}()
	<-fastDone // 慢键在途时快键必须能完成
	close(slowRelease)
}

func TestFailureNotCachedAndRetryable(t *testing.T) {
	g := New[int](0)
	boom := errors.New("boom")
	calls := 0
	fn := func(ctx context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, boom
		}
		return 7, nil
	}
	if _, err := g.Do(context.Background(), "k", fn); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	v, err := g.Do(context.Background(), "k", fn)
	if err != nil || v != 7 {
		t.Fatalf("retry got %v, %v", v, err)
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want 2 (failure must not be cached)", calls)
	}
}

func TestInflightLimit(t *testing.T) {
	g := New[int](1)
	started := make(chan struct{})
	release := make(chan struct{})
	go g.Do(context.Background(), "a", func(ctx context.Context) (int, error) {
		close(started)
		<-release
		return 1, nil
	})
	<-started
	_, err := g.Do(context.Background(), "b", func(ctx context.Context) (int, error) {
		return 2, nil
	})
	if !errors.Is(err, ErrTooManyInflight) {
		t.Fatalf("want ErrTooManyInflight, got %v", err)
	}
	// 同键合并不受上限影响。
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := g.Do(context.Background(), "a", func(ctx context.Context) (int, error) {
			return 9, nil
		}); err != nil {
			t.Errorf("same-key wait should merge, got %v", err)
		}
	}()
	close(release)
	<-done
}

func TestWaitCancellable(t *testing.T) {
	g := New[int](0)
	started := make(chan struct{})
	release := make(chan struct{})
	go g.Do(context.Background(), "k", func(ctx context.Context) (int, error) {
		close(started)
		<-release
		return 1, nil
	})
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.Do(ctx, "k", func(ctx context.Context) (int, error) {
		return 2, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	close(release)
	// 取消等待不影响在途执行：之后同键已完成，再次 Do 重新执行。
	if got := g.Calls(); got != 1 {
		t.Fatalf("calls=%d want 1", got)
	}
}
