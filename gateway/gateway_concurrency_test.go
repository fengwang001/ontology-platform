package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/record"
)

// blockingExec returns an ExecuteFunc that blocks until release is
// closed, plus a channel that closes once the exec has started.
func blockingExec(result string) (ExecuteFunc, <-chan struct{}, chan<- struct{}) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	exec := func(context.Context, []byte) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return []byte(result), nil
	}
	return exec, started, release
}

func TestConcurrentSameKeySameBodyJoins(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	exec, started, release := blockingExec("r1")
	const n = 16
	outs := make([]Outcome, n)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i] = g.Submit(context.Background(), "k1", []byte("b"), exec)
		}(i)
	}
	<-started
	close(release)
	wg.Wait()
	replays := 0
	for i, out := range outs {
		if out.Err != nil || string(out.Result) != "r1" {
			t.Fatalf("joiner %d got %+v", i, out)
		}
		if out.Replayed {
			replays++
		}
	}
	if replays != n-1 {
		t.Fatalf("replays = %d, want %d (exactly one first execution)", replays, n-1)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
}

func TestInFlightConflictReturnsImmediately(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	exec, started, release := blockingExec("r1")
	done := make(chan Outcome, 1)
	go func() { done <- g.Submit(context.Background(), "k1", []byte("a"), exec) }()
	<-started
	conflict := make(chan Outcome, 1)
	go func() { conflict <- g.Submit(context.Background(), "k1", []byte("b"), okExec("x")) }()
	select {
	case out := <-conflict:
		if !errors.Is(out.Err, ErrConflict) {
			t.Fatalf("err = %v, want conflict", out.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight conflict must not block")
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
	close(release)
	if out := <-done; out.Err != nil || string(out.Result) != "r1" {
		t.Fatalf("first execution = %+v", out)
	}
}

func TestExpiryDoesNotAffectInFlight(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.now, time.Minute)
	exec, started, release := blockingExec("r1")
	done := make(chan Outcome, 1)
	go func() { done <- g.Submit(context.Background(), "k1", []byte("b"), exec) }()
	<-started
	clock.advance(10 * time.Minute) // far past TTL while in-flight
	if st := g.Inspect("k1"); !st.Exists || st.State != record.StateInFlight {
		t.Fatalf("in-flight record must survive TTL: %+v", st)
	}
	conflict := g.Submit(context.Background(), "k1", []byte("other"), okExec("x"))
	if !errors.Is(conflict.Err, ErrConflict) {
		t.Fatalf("in-flight past TTL must still conflict, got %+v", conflict)
	}
	joined := make(chan Outcome, 1)
	go func() { joined <- g.Submit(context.Background(), "k1", []byte("b"), okExec("x")) }()
	time.Sleep(50 * time.Millisecond) // let the joiner reach Wait before release
	close(release)
	if out := <-joined; !out.Replayed || string(out.Result) != "r1" {
		t.Fatalf("in-flight record must still be joinable, got %+v", out)
	}
	if out := <-done; out.Err != nil {
		t.Fatalf("first execution = %+v", out)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
}

func TestSlowKeyDoesNotBlockOtherKeys(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	exec, started, release := blockingExec("slow")
	defer close(release)
	go func() { g.Submit(context.Background(), "slow-key", []byte("b"), exec) }()
	<-started
	done := make(chan Outcome, 1)
	go func() { done <- g.Submit(context.Background(), "fast-key", []byte("b"), okExec("fast")) }()
	select {
	case out := <-done:
		if out.Err != nil || string(out.Result) != "fast" {
			t.Fatalf("fast key = %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("other keys must not be blocked by a slow execution")
	}
}

func TestConcurrentMixedKeysAndBodies(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i%4)
			body := []byte(fmt.Sprintf("body-%d", i%2))
			out := g.Submit(context.Background(), key, body, okExec("r"))
			if out.Err != nil && !errors.Is(out.Err, ErrConflict) {
				t.Errorf("unexpected err: %v", out.Err)
			}
		}(i)
	}
	wg.Wait()
	if got := g.ExecCount(); got > 8 {
		t.Fatalf("ExecCount = %d, want <= 8 (4 keys x 2 bodies, conflicts excluded)", got)
	}
}
