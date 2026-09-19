// Command demo exercises every semantic guarantee of the idem package and
// prints one OK/FAIL line per guarantee. Exit code is 0 only if all pass.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/idem"
)

var now = time.Unix(1_700_000_000, 0)

func clock() time.Time { return now }

func main() {
	failed := 0
	check := func(name string, ok bool) {
		status := "OK"
		if !ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%-4s %s\n", status, name)
	}

	// 1. Replay: second call never re-runs fn.
	e := idem.New(clock, time.Minute)
	calls := 0
	fn := func() (idem.Result, error) { calls++; return idem.Result{Code: 200, Body: "r1"}, nil }
	r1, _, _ := e.Do("k1", "fp", fn)
	r2, o2, _ := e.Do("k1", "fp", fn)
	check("replay: same key+fp replays without re-executing", calls == 1 && o2 == idem.Replayed && r1 == r2)

	// 2. Fingerprint conflict: sentinel error, record untouched.
	_, _, err := e.Do("k1", "other", fn)
	r3, o3, _ := e.Do("k1", "fp", fn)
	check("fingerprint mismatch: sentinel, no overwrite", errors.Is(err, idem.ErrFingerprintMismatch) && o3 == idem.Replayed && r3 == r1 && calls == 1)

	// 3a. Business error is cached and replayed.
	biz := errors.New("biz")
	e.Do("k2", "fp", func() (idem.Result, error) { return idem.Result{Code: 422}, biz })
	_, _, err = e.Do("k2", "fp", fn)
	check("business error: cached and replayed", errors.Is(err, biz) && calls == 1)

	// 3b. Retriable error is not cached; slot freed.
	e.Do("k3", "fp", func() (idem.Result, error) { return idem.Result{}, idem.Retriable(errors.New("db")) })
	_, o, _ := e.Do("k3", "fp", fn)
	check("retriable error: not cached, re-executes", o == idem.Executed && calls == 2)

	// 4. Panic is recovered, key not poisoned.
	_, _, err = e.Do("k4", "fp", func() (idem.Result, error) { panic("boom") })
	_, o, err2 := e.Do("k4", "fp", fn)
	check("panic: returned as error, key reusable", err != nil && err2 == nil && o == idem.Executed)

	// 5. Single flight: N concurrent callers, one execution, rest wait.
	var runs atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	outs := make([]idem.Outcome, 16)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, outs[i], _ = e.Do("k5", "fp", func() (idem.Result, error) {
				runs.Add(1)
				<-release
				return idem.Result{Code: 200}, nil
			})
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	waited := 0
	for _, oc := range outs {
		if oc == idem.Waited {
			waited++
		}
	}
	check("single flight: 1 execution, 15 waiters", runs.Load() == 1 && waited == 15)

	// 6. Different keys never block each other.
	block := make(chan struct{})
	go e.Do("k6a", "fp", func() (idem.Result, error) { <-block; return idem.Result{}, nil })
	time.Sleep(20 * time.Millisecond)
	doneCh := make(chan struct{})
	go func() { e.Do("k6b", "fp", fn); close(doneCh) }()
	ok := true
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		ok = false
	}
	close(block)
	check("isolation: slow key does not block other keys", ok)

	// 7. TTL is half-open on completion+ttl, from the injected clock.
	now = now.Add(time.Minute - time.Nanosecond)
	_, o, _ = e.Do("k1", "fp", fn)
	before := o == idem.Replayed
	now = now.Add(time.Nanosecond)
	_, o, _ = e.Do("k1", "fp", fn)
	check("ttl: replay before deadline, re-execute at it", before && o == idem.Executed)

	// 8. In-flight calls never expire.
	now = now.Add(time.Hour)
	inflight := make(chan struct{})
	go e.Do("k8", "fp", func() (idem.Result, error) { <-inflight; return idem.Result{Code: 200}, nil })
	time.Sleep(20 * time.Millisecond)
	now = now.Add(time.Hour)
	waitCh := make(chan idem.Outcome, 1)
	go func() { _, oc, _ := e.Do("k8", "fp", fn); waitCh <- oc }()
	time.Sleep(20 * time.Millisecond)
	close(inflight)
	check("in-flight: never expires mid-execution", <-waitCh == idem.Waited)

	if failed > 0 {
		fmt.Printf("%d check(s) FAILED\n", failed)
		os.Exit(1)
	}
	fmt.Println("all 8 semantics OK")
}
