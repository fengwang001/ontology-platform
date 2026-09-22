package gateway

import (
	"errors"
	"sync"
	"testing"
)

// TestConcurrentDuplicatesJoinSingleExecution fires many goroutines at
// the same key with the same body while the execution is blocked; all
// must observe the same outcome and the exec must run exactly once.
func TestConcurrentDuplicatesJoinSingleExecution(t *testing.T) {
	g, _ := newGateway()
	body := []byte("join-me")
	release := make(chan struct{})
	started := make(chan struct{})
	const n = 32
	var wg sync.WaitGroup
	results := make(chan Result, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := g.Submit("k", body, func() (any, error) {
				close(started)
				<-release
				return "joined", nil
			})
			if err != nil {
				t.Errorf("submit err = %v", err)
				return
			}
			results <- res
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	replays := 0
	for res := range results {
		if res.Value != "joined" {
			t.Fatalf("value = %v, want joined", res.Value)
		}
		if res.Replayed {
			replays++
		}
	}
	if replays != n-1 {
		t.Fatalf("replays = %d, want %d", replays, n-1)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
}

// TestInFlightConflictReturnsImmediately submits a conflicting body
// while the first execution is still blocked; it must fail fast with
// ErrConflict and must not wait for the in-flight execution.
func TestInFlightConflictReturnsImmediately(t *testing.T) {
	g, _ := newGateway()
	release := make(chan struct{})
	started := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = g.Submit("k", []byte("a"), func() (any, error) {
			close(started)
			<-release
			return "va", nil
		})
	}()
	<-started
	// The first execution is still blocked; the conflicting submit must
	// return before we release it.
	_, err := g.Submit("k", []byte("b"), okExec("vb"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	select {
	case <-firstDone:
		t.Fatal("conflicting submit must not wait for in-flight execution")
	default:
	}
	close(release)
	<-firstDone
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
	res, err := g.Submit("k", []byte("a"), okExec("other"))
	if err != nil || res.Value != "va" {
		t.Fatalf("stored result clobbered: res = %+v, err = %v", res, err)
	}
}

// TestSlowKeyDoesNotBlockOthers proves no global lock is held while an
// execution runs: key B completes while key A is still blocked.
func TestSlowKeyDoesNotBlockOthers(t *testing.T) {
	g, _ := newGateway()
	release := make(chan struct{})
	started := make(chan struct{})
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		_, _ = g.Submit("slow", []byte("a"), func() (any, error) {
			close(started)
			<-release
			return "slow-v", nil
		})
	}()
	<-started
	res, err := g.Submit("fast", []byte("b"), okExec("fast-v"))
	if err != nil || res.Value != "fast-v" {
		t.Fatalf("fast key res = %+v, err = %v", res, err)
	}
	select {
	case <-aDone:
		t.Fatal("slow key finished before fast key: global lock suspected")
	default:
	}
	close(release)
	<-aDone
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("ExecCount = %d, want 2", got)
	}
}

// TestMixedConcurrentLoad hammers the gateway with same-key/same-body,
// same-key/different-body, and different-key submits to shake out data
// races under -race.
func TestMixedConcurrentLoad(t *testing.T) {
	g, _ := newGateway()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "shared"
			body := []byte("same")
			if i%3 == 0 {
				body = []byte("different")
			}
			if i%5 == 0 {
				key = "solo"
			}
			_, _ = g.Submit(key, body, okExec(i))
			_ = g.Query(key)
		}(i)
	}
	wg.Wait()
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("ExecCount = %d, want 2 (shared + solo)", got)
	}
}
