package gateway

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/record"
)

// blockingFn returns an execute function that signals when it starts and
// then blocks until release is closed.
func blockingFn(started chan<- struct{}, release <-chan struct{}, value any) func() (any, error) {
	return func() (any, error) {
		close(started)
		<-release
		return value, nil
	}
}

func TestConcurrentDuplicatesCoalesce(t *testing.T) {
	g, _ := newGateway()
	started := make(chan struct{})
	release := make(chan struct{})
	body := []byte("body")

	const n = 16
	results := make(chan Result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := g.Submit("k1", body, blockingFn(started, release, "v1"))
			if err != nil {
				t.Errorf("submit: %v", err)
				return
			}
			results <- res
		}()
	}

	<-started // wait until the single execution is in flight
	close(release)
	wg.Wait()
	close(results)

	executed := 0
	for res := range results {
		if res.Value != "v1" {
			t.Fatalf("coalesced result = %v, want v1", res.Value)
		}
		if !res.Replayed {
			executed++
		}
	}
	if executed != 1 {
		t.Fatalf("exactly one submitter must see the live execution, saw %d", executed)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("exec count = %d, want 1", got)
	}
}

func TestInFlightConflictReturnsImmediately(t *testing.T) {
	g, _ := newGateway()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	go func() {
		_, _ = g.Submit("k1", []byte("body-A"), blockingFn(started, release, "v"))
	}()
	<-started // first execution is now blocked in flight

	// Different body must conflict at once, without waiting for the
	// running execution. If it blocked, this test would deadlock and
	// be caught by the go test timeout.
	_, err := g.Submit("k1", []byte("body-B"), valueOf("unreachable"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("in-flight different body: got %v, want ErrConflict", err)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("conflict must not execute, exec count = %d, want 1", got)
	}
}

func TestExpiryDoesNotAffectInFlight(t *testing.T) {
	g, clock := newGateway()
	started := make(chan struct{})
	release := make(chan struct{})
	body := []byte("body")

	go func() {
		_, _ = g.Submit("k1", body, blockingFn(started, release, "v1"))
	}()
	<-started

	clock.Advance(100 * testTTL) // far past the deadline, still in flight

	st, ok := g.Lookup("k1")
	if !ok || st.State != record.InFlight {
		t.Fatalf("in-flight record must survive expiry, got (%+v, %v)", st, ok)
	}

	// A duplicate of the in-flight key must coalesce (wait), not start
	// a new execution, even though the TTL has long passed.
	done := make(chan Result, 1)
	go func() {
		res, err := g.Submit("k1", body, valueOf("unreachable"))
		if err != nil {
			t.Errorf("duplicate submit: %v", err)
		}
		done <- res
	}()

	// Give the duplicate a chance to either execute (bug) or coalesce.
	time.Sleep(50 * time.Millisecond)
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("expired in-flight duplicate must not execute, count = %d", got)
	}

	close(release)
	res := <-done
	if !res.Replayed || res.Value != "v1" {
		t.Fatalf("duplicate must replay the in-flight result, got (%v, replayed=%v)", res.Value, res.Replayed)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("exec count = %d, want 1", got)
	}
}

func TestSlowExecutionDoesNotBlockOtherKeys(t *testing.T) {
	g, _ := newGateway()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	go func() {
		_, _ = g.Submit("slow", []byte("a"), blockingFn(started, release, "slow-v"))
	}()
	<-started // key "slow" is now stuck inside its execute function

	// Another key must complete normally: no global lock is held while
	// the execute function runs. A deadlock here fails via test timeout.
	res, err := g.Submit("fast", []byte("b"), valueOf("fast-v"))
	if err != nil || res.Replayed || res.Value != "fast-v" {
		t.Fatalf("other key must proceed during slow execution, got (%v, %v)", res.Value, err)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("exec count = %d, want 2", got)
	}
}

func TestConcurrentMixedStress(t *testing.T) {
	g, _ := newGateway()
	const workers = 32
	const rounds = 25

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", w%4)
			for r := 0; r < rounds; r++ {
				_, _ = g.Submit(key, []byte("body"), valueOf("v"))
				_, _ = g.Submit(key, []byte("alien"), valueOf("x")) // always conflicts
				_, _ = g.Lookup(key)
			}
		}(w)
	}
	wg.Wait()

	if got := g.ExecCount(); got != 4 {
		t.Fatalf("exec count = %d, want 4 (one per key)", got)
	}
	for i := 0; i < 4; i++ {
		res, err := g.Submit(fmt.Sprintf("key-%d", i), []byte("body"), valueOf("unreachable"))
		if err != nil || !res.Replayed || res.Value != "v" {
			t.Fatalf("key-%d final replay = (%v, %v, replayed=%v)", i, res.Value, err, res.Replayed)
		}
	}
}
