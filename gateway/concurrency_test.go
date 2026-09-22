package gateway

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingExec returns an ExecFunc plus channels to observe and release it.
func blockingExec(value string) (ExecFunc, <-chan struct{}, chan<- struct{}) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	exec := func(body []byte) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return []byte(value), nil
	}
	return exec, started, release
}

func TestConcurrentDuplicatesMerge(t *testing.T) {
	g, _ := newGateway()
	exec, started, release := blockingExec("merged")
	const n = 8
	outs := make([]Outcome, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i], errs[i] = g.Submit("k", []byte("same-body"), exec)
		}(i)
	}
	<-started
	close(release)
	wg.Wait()
	fresh := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("submit %d: %v", i, errs[i])
		}
		if string(outs[i].Value) != "merged" {
			t.Fatalf("submit %d = %q, want merged", i, outs[i].Value)
		}
		if !outs[i].Replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("exactly one submit may execute, got %d", fresh)
	}
	if got := g.ExecCalls(); got != 1 {
		t.Fatalf("ExecCalls = %d, want 1", got)
	}
}

func TestInFlightConflictReturnsImmediately(t *testing.T) {
	g, _ := newGateway()
	exec, started, release := blockingExec("slow")
	done := make(chan error, 1)
	go func() {
		_, err := g.Submit("k", []byte("body-a"), exec)
		done <- err
	}()
	<-started
	// The first execution is still blocked; a different body must fail fast.
	if _, err := g.Submit("k", []byte("body-b"), okExec("B")); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict while in-flight", err)
	}
	select {
	case <-done:
		t.Fatal("conflicting submit must not disturb the in-flight execution")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if got := g.ExecCalls(); got != 1 {
		t.Fatalf("ExecCalls = %d, want 1", got)
	}
}

func TestExpiryDoesNotTouchInFlight(t *testing.T) {
	g, clk := newGateway()
	exec, started, release := blockingExec("kept")
	done := make(chan Outcome, 1)
	go func() {
		out, _ := g.Submit("k", []byte("body"), exec)
		done <- out
	}()
	<-started
	clk.advance(1000 * time.Hour) // far beyond the TTL
	dup := make(chan Outcome, 1)
	go func() {
		out, _ := g.Submit("k", []byte("body"), okExec("other"))
		dup <- out
	}()
	close(release)
	first, second := <-done, <-dup
	if first.Replayed || string(first.Value) != "kept" {
		t.Fatalf("first = %+v, want fresh kept", first)
	}
	if !second.Replayed || string(second.Value) != "kept" {
		t.Fatalf("duplicate during in-flight = %+v, want replay of kept", second)
	}
	if got := g.ExecCalls(); got != 1 {
		t.Fatalf("expiry must not affect in-flight, ExecCalls = %d", got)
	}
}

func TestSlowKeyDoesNotBlockOthers(t *testing.T) {
	g, _ := newGateway()
	exec, started, release := blockingExec("slow")
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.Submit("slow-key", []byte("a"), exec)
	}()
	<-started
	out, err := g.Submit("fast-key", []byte("b"), okExec("fast"))
	if err != nil || string(out.Value) != "fast" {
		t.Fatalf("fast key blocked by slow key: %+v, %v", out, err)
	}
	select {
	case <-done:
		t.Fatal("slow key finished before its release")
	default:
	}
	close(release)
	<-done
}

func TestConcurrentMixedKeysAndBodies(t *testing.T) {
	g, _ := newGateway()
	var wg sync.WaitGroup
	errs := make([]error, 16)
	outs := make([]Outcome, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + i%3))
			body := []byte{byte(i % 2)}
			outs[i], errs[i] = g.Submit(key, body, okExec("v"))
		}(i)
	}
	wg.Wait()
	for i := 0; i < 16; i++ {
		if errs[i] != nil && !errors.Is(errs[i], ErrConflict) {
			t.Fatalf("submit %d: unexpected err %v", i, errs[i])
		}
		if errs[i] == nil && string(outs[i].Value) != "v" {
			t.Fatalf("submit %d = %q, want v", i, outs[i].Value)
		}
	}
	if got := g.ExecCalls(); got > 3 {
		t.Fatalf("ExecCalls = %d, want at most one per key", got)
	}
}
