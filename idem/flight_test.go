package idem

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Semantics 5: N concurrent callers with the same key/fp cause exactly one fn
// execution; every other caller blocks and receives Waited with the same
// result.
func TestSingleFlightSameKey(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	const n = 32
	var running atomic.Int32
	var maxRunning atomic.Int32
	var executions atomic.Int32

	started := make(chan struct{})
	release := make(chan struct{})
	fn := func() (Result, error) {
		cur := running.Add(1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		executions.Add(1)
		close(started)
		<-release
		return Result{Code: 200, Body: "shared"}, nil
	}

	var wg sync.WaitGroup
	outcomes := make([]Outcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, outcomes[idx], errs[idx] = ex.Do("k", "fp", fn)
		}(i)
	}

	<-started
	close(release)
	wg.Wait()

	var executed, waited int
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d got error: %v", i, errs[i])
		}
		switch outcomes[i] {
		case Executed:
			executed++
		case Waited:
			waited++
		default:
			t.Fatalf("caller %d unexpected outcome %v", i, outcomes[i])
		}
	}
	if executed != 1 || waited != n-1 {
		t.Fatalf("executed=%d waited=%d", executed, waited)
	}
	if executions.Load() != 1 || maxRunning.Load() != 1 {
		t.Fatalf("executions=%d maxConcurrent=%d", executions.Load(), maxRunning.Load())
	}
}

// Semantics 6: a long-running call for one key never blocks another key.
func TestDifferentKeysDoNotBlock(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	block := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		_, _, _ = ex.Do("slow", "fp", func() (Result, error) {
			close(entered)
			<-block
			return Result{}, nil
		})
	}()

	<-entered
	done := make(chan Result, 1)
	go func() {
		res, _, err := ex.Do("fast", "fp", func() (Result, error) {
			return Result{Code: 200, Body: "immediate"}, nil
		})
		if err != nil {
			t.Errorf("fast key error: %v", err)
		}
		done <- res
	}()

	select {
	case res := <-done:
		if res.Body != "immediate" {
			t.Fatalf("unexpected %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("different key was blocked by the in-flight key")
	}
	close(block)
}
