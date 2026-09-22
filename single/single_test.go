package single

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"ontology/entry"
)

func okFn(v string) FetchFunc {
	return func(context.Context) (entry.FetchResult, error) {
		return entry.FetchResult{Value: v, Version: 1, Found: true}, nil
	}
}

func TestDoMergesConcurrentCallers(t *testing.T) {
	g := New(0)
	const n = 16
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	fn := func(context.Context) (entry.FetchResult, error) {
		close(started)
		<-release
		return entry.FetchResult{Value: "v", Version: 1, Found: true}, nil
	}
	results := make(chan entry.FetchResult, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := g.Do(context.Background(), "k", fn)
			if err != nil {
				t.Error(err)
				return
			}
			results <- res
		}()
	}
	<-started
	for i := 0; i < 10000 && g.Waiting("k") < n; i++ {
		runtime.Gosched()
	}
	if got := g.Waiting("k"); got != n {
		t.Fatalf("waiters = %d, want %d", got, n)
	}
	close(release)
	wg.Wait()
	close(results)
	for res := range results {
		if res.Value != "v" {
			t.Fatalf("res = %v", res)
		}
	}
	if g.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", g.Calls())
	}
}

func TestFailureNotCachedAndRetryable(t *testing.T) {
	g := New(0)
	boom := errors.New("boom")
	calls := 0
	fn := func(context.Context) (entry.FetchResult, error) {
		calls++
		if calls == 1 {
			return entry.FetchResult{}, boom
		}
		return entry.FetchResult{Value: "ok", Version: 1, Found: true}, nil
	}
	if _, err := g.Do(context.Background(), "k", fn); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	res, err := g.Do(context.Background(), "k", fn)
	if err != nil || res.Value != "ok" {
		t.Fatalf("retry = %v, %v", res, err)
	}
	if calls != 2 || g.Calls() != 2 {
		t.Fatalf("calls = %d/%d, want 2", calls, g.Calls())
	}
}

func TestDistinctKeysRunConcurrently(t *testing.T) {
	g := New(0)
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	defer close(slowRelease)
	go func() {
		_, _ = g.Do(context.Background(), "slow", func(context.Context) (entry.FetchResult, error) {
			close(slowStarted)
			<-slowRelease
			return entry.FetchResult{Version: 1, Found: true}, nil
		})
	}()
	<-slowStarted
	res, err := g.Do(context.Background(), "fast", okFn("f"))
	if err != nil || res.Value != "f" {
		t.Fatalf("fast key blocked: %v %v", res, err)
	}
}

func TestInflightLimit(t *testing.T) {
	g := New(1)
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "a", func(context.Context) (entry.FetchResult, error) {
			close(started)
			<-release
			return entry.FetchResult{Version: 1, Found: true}, nil
		})
	}()
	<-started
	_, err := g.Do(context.Background(), "b", okFn("b"))
	if !errors.Is(err, ErrInflightFull) {
		t.Fatalf("err = %v", err)
	}
	// Joining the in-flight key is allowed even at the limit.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.Do(context.Background(), "a", okFn("a"))
	}()
	close(release)
	<-done
}

func TestContextCancelAbandonsWait(t *testing.T) {
	g := New(0)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _ = g.Do(context.Background(), "k", func(context.Context) (entry.FetchResult, error) {
			close(started)
			<-release
			return entry.FetchResult{Version: 1, Found: true}, nil
		})
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.Do(ctx, "k", okFn("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
