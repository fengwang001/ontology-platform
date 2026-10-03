package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentPublicCoalesce(t *testing.T) {
	c := New(1000, 4)
	var calls int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	fetch := func(_ context.Context, _ Request) (FetchResult, error) {
		atomic.AddInt32(&calls, 1)
		started <- struct{}{}
		<-release
		return pub(200, 1000, 10), nil
	}
	const n = 6
	var wg sync.WaitGroup
	srcs := make([]Source, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Different subjects and different auth/cookie headers must
			// still coalesce; other headers must be identical.
			hdr := map[string]string{"authorization": "tok", "cookie": "c", "x": "1"}
			req := gq("/p", "u"+string(rune('0'+i)), hdr)
			_, srcs[i], errs[i] = c.Get(context.Background(), req, 0, fetch)
		}(i)
	}
	<-started // leader fetch in progress
	// Wait until the other n-1 goroutines registered as coalesce waiters.
	for i := 0; i < n; i++ {
		select {
		case <-c.joinSignal:
		case <-time.After(2 * time.Second):
			t.Fatal("goroutine never registered")
		}
	}
	close(release)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("public concurrent misses must coalesce to 1 fetch, got %d", calls)
	}
	miss, shared := 0, 0
	for _, src := range srcs {
		switch src {
		case Miss:
			miss++
		case Shared:
			shared++
		}
	}
	if miss != 1 || shared != n-1 {
		t.Fatalf("want exactly 1 Miss and %d Shared, got miss=%d shared=%d srcs=%v", n-1, miss, shared, srcs)
	}
	if st := c.StatsSnapshot(); st.Fetches != 1 || st.Shared != n-1 {
		t.Fatalf("stats=%+v", st)
	}
}

func TestConcurrentPrivateWaitersEachFetch(t *testing.T) {
	c := New(1000, 4)
	var calls int32
	release := make(chan struct{})
	fetch := func(_ context.Context, req Request) (FetchResult, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return priv(200, 1000, 10), nil
	}
	const n = 4
	var wg sync.WaitGroup
	srcs := make([]Source, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, srcs[i], _ = c.Get(context.Background(), gq("/p", "u", nil), 0, fetch)
		}(i)
	}
	// Wait until every goroutine registered (leader + queued waiters),
	// then for the leader to enter fetch; every waiter must issue its own
	// fetch once the Private leader result arrives.
	for i := 0; i < n; i++ {
		select {
		case <-c.joinSignal:
		case <-time.After(2 * time.Second):
			t.Fatal("goroutine never registered")
		}
	}
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&calls) == 0 {
		select {
		case <-deadline:
			t.Fatal("leader never started")
		default:
		}
	}
	close(release)
	wg.Wait()
	if calls != n {
		t.Fatalf("private result forces %d fetches, got %d", n, calls)
	}
	for i, src := range srcs {
		if src != Miss {
			t.Fatalf("waiter %d source=%s want Miss", i, src)
		}
	}
}

func TestConcurrentErrorShared(t *testing.T) {
	c := New(1000, 4)
	var calls int32
	release := make(chan struct{})
	fetch := func(_ context.Context, _ Request) (FetchResult, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return FetchResult{}, errBoom
	}
	const n = 4
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := c.Get(context.Background(), gq("/p", "", nil), 0, fetch)
			if !errors.Is(err, errBoom) {
				t.Errorf("waiters must receive the same error, got %v", err)
			}
		}()
	}
	for atomic.LoadInt32(&calls) == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("error fetch coalesces to 1 call, got %d", calls)
	}
	if c.Bytes() != 0 {
		t.Fatalf("error result must not be stored")
	}
}

func TestOtherPathsNotBlocked(t *testing.T) {
	c := New(1000, 4)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	fetch := func(_ context.Context, req Request) (FetchResult, error) {
		if req.Path == "/slow" {
			started <- struct{}{}
			<-release
			return pub(200, 100, 10), nil
		}
		return pub(200, 100, 10), nil
	}
	go func() {
		_, _, _ = c.Get(context.Background(), gq("/slow", "", nil), 0, fetch)
	}()
	<-started
	// While /slow fetch is in flight, /fast must complete independently.
	done := make(chan Source, 1)
	go func() {
		_, src, err := c.Get(context.Background(), gq("/fast", "", nil), 0, fetch)
		if err != nil {
			t.Error(err)
		}
		done <- src
	}()
	select {
	case src := <-done:
		if src != Miss {
			t.Fatalf("/fast src=%s", src)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("other path must not be blocked during origin fetch")
	}
	close(release)
}
