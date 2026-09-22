package replica

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"ontology/bus"
	"ontology/entry"
)

// Requirement 4: N concurrent misses for one key merge into a single
// backend fetch and all observers get the same result.
func TestConcurrentMissMergesIntoOneFetch(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "v1")
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.be.hook = func(key string) {
		if key != "k" {
			return
		}
		once.Do(func() { close(started) })
		<-release
	}
	const n = 16
	results := make(chan ReadResult, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.rep.Read(context.Background(), "k")
			if err != nil {
				t.Error(err)
				return
			}
			results <- res
		}()
	}
	<-started
	for i := 0; i < 100000 && f.rep.group.Waiting("k") < n; i++ {
		runtime.Gosched()
	}
	if got := f.rep.group.Waiting("k"); got != n {
		t.Fatalf("waiters = %d, want %d", got, n)
	}
	close(release)
	wg.Wait()
	close(results)
	for res := range results {
		if res.State != Hit || res.Value != "v1" || res.Version != 1 {
			t.Fatalf("res = %+v", res)
		}
	}
	if got := f.be.loadCalls(); got != 1 {
		t.Fatalf("load calls = %d, want 1", got)
	}
	if got := f.rep.FetchCalls(); got != 1 {
		t.Fatalf("fetch flights = %d, want 1", got)
	}
}

// Requirement 4: a slow key must not block fetches of other keys.
func TestSlowKeyDoesNotBlockOthers(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("slow", "s")
	f.be.set("fast", "f")
	started := make(chan struct{})
	release := make(chan struct{})
	f.be.hook = func(key string) {
		if key == "slow" {
			close(started)
			<-release
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mustRead(t, f.rep, "slow")
	}()
	<-started
	res := mustRead(t, f.rep, "fast")
	if res.State != Hit || res.Value != "f" {
		t.Fatalf("fast read = %+v", res)
	}
	close(release)
	<-done
}

// Requirement 5: a failed fetch caches nothing, every waiter sees
// the error, the key is immediately refetchable, and a retry really
// hits the backend again.
func TestFailedFetchNotCached(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "v1")
	boom := errors.New("backend down")
	f.be.fail = boom
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.be.hook = func(key string) {
		once.Do(func() { close(started) })
		<-release
	}

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.rep.Read(context.Background(), "k")
			errs <- err
		}()
	}
	<-started
	for i := 0; i < 100000 && f.rep.group.Waiting("k") < n; i++ {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("waiter err = %v", err)
		}
	}
	info := f.rep.Inspect("k")
	if info.State != entry.Hole {
		t.Fatalf("state after failure = %v", info.State)
	}
	if got := f.be.loadCalls(); got != 1 {
		t.Fatalf("load calls = %d, want 1 (merged)", got)
	}

	f.be.fail = nil
	res := mustRead(t, f.rep, "k")
	if res.State != Hit || res.Value != "v1" {
		t.Fatalf("retry = %+v", res)
	}
	if got := f.be.loadCalls(); got != 2 {
		t.Fatalf("load calls = %d, want 2 (retry refetched)", got)
	}
}

// Requirement 6: an invalidation arriving mid-flight must cause the
// in-flight fetch's stale result to be discarded, never stored.
func TestMidFlightInvalidationDiscardsStaleResult(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "old") // backend at v1
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.be.hook = func(key string) {
		// Only the first (in-flight) fetch blocks; it returns the
		// value as of fetch start: "old" at v1.
		once.Do(func() { close(started); <-release })
	}
	readDone := make(chan ReadResult, 1)
	go func() {
		res, err := f.rep.Read(context.Background(), "k")
		if err != nil {
			t.Errorf("read: %v", err)
		}
		readDone <- res
	}()
	<-started // fetch in flight
	// Backend moves to v2 and the notification arrives mid-flight.
	f.be.set("k", "new")
	if err := f.rep.ApplyNotification(bus.Notification{Key: "k", Version: 2}); err != nil {
		t.Fatal(err)
	}
	// The in-flight loader still returns the old v1 snapshot.
	f.be.mu.Lock()
	f.be.vals["k"] = "old"
	f.be.vers["k"] = 1
	f.be.mu.Unlock()
	close(release)

	res := <-readDone
	if res.State == Hit || res.Value == "old" {
		t.Fatalf("stale value served: %+v", res)
	}
	info := f.rep.Inspect("k")
	if info.State == entry.Valid {
		t.Fatalf("stale result was cached: %+v", info)
	}
	if info.Version != 2 {
		t.Fatalf("known version = %v, want 2", info.Version)
	}
	// Next read fetches the current backend value.
	f.be.set("k", "new")
	res = mustRead(t, f.rep, "k")
	if res.State != Hit || res.Value != "new" || res.Version != 3 {
		t.Fatalf("refetch = %+v", res)
	}
}
