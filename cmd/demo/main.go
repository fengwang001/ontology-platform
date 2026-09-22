// Demo for the in-flight request correlator. Run with:
//
//	go run ./cmd/demo
//
// It uses an injected clock, never touches the network or filesystem, takes
// no arguments and exits 0. Each check prints one OK/FAIL line.
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/correlate"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
			return
		}
		fails++
		fmt.Println("FAIL " + name)
	}

	clk := &clock{t: time.Unix(0, 0)}
	cor := correlate.New(2, clk.now)

	// 1. Normal association and 2. out-of-order replies.
	a, _ := cor.Issue(10 * time.Second)
	b, _ := cor.Issue(10 * time.Second)
	check("normal associate then out-of-order deliver (b then a)",
		cor.Deliver(b) == nil && cor.Deliver(a) == nil && cor.InFlight() == 0)

	// 3. The headline scenario on its own clock: t=0 issue, t=10 timeout,
	// t=11 reuse, t=12 late reply of the first generation.
	sclk := &clock{t: time.Unix(0, 0)}
	scor := correlate.New(1, sclk.now)
	first, err := scor.Issue(10 * time.Second)
	firstOK := err == nil && first.ID == 0 && first.Epoch == 1
	sclk.add(10 * time.Second)
	_, stillWaiting := scor.Lookup(first.ID)
	timedOut := scor.Counters().TimedOut == 1
	sclk.add(time.Second)
	second, err2 := scor.Issue(10 * time.Second)
	reused := err2 == nil && second.ID == 0 && second.Epoch == 2
	sclk.add(time.Second)
	late := errors.Is(scor.Deliver(first), correlate.ErrOrphanStale)
	_, newerWaiting := scor.Lookup(second.ID)
	check("late reply after timeout+reuse is stale, never binds (spec sample)",
		firstOK && !stillWaiting && timedOut && reused && late && newerWaiting)

	// 4. Timeout boundary, left-closed right-open at the exact deadline.
	bclk := &clock{t: time.Unix(0, 0)}
	bcor := correlate.New(1, bclk.now)
	only, _ := bcor.Issue(10 * time.Second)
	bclk.add(9 * time.Second) // one instant before deadline: still waiting
	_, alive := bcor.Lookup(only.ID)
	bclk.add(time.Second) // now == deadline: timed out
	_, dead := bcor.Lookup(only.ID)
	check("timeout left-closed: alive just before, timed out at deadline",
		alive && !dead && bcor.Counters().TimedOut == 1)

	// 5. The three orphan classes are mutually distinguishable.
	u := errors.Is(cor.Deliver(correlate.Token{ID: 42}), correlate.ErrOrphanUnknown)
	done, _ := cor.Issue(time.Minute)
	cor.Deliver(done)
	i := errors.Is(cor.Deliver(done), correlate.ErrOrphanIdle)
	newGen, _ := cor.Issue(time.Minute)
	s := newGen.ID == done.ID &&
		errors.Is(cor.Deliver(done), correlate.ErrOrphanStale)
	snap := cor.Counters()
	classes := u && i && s && snap.OrphanUnknown >= 1 &&
		snap.OrphanIdle >= 1 && snap.OrphanStale >= 1
	check("three orphan classes distinct and counted separately", classes)

	// 6. Capacity rejection leaves every state and counter unchanged.
	cap2 := correlate.New(1, clk.now)
	x, _ := cap2.Issue(time.Minute)
	before := cap2.SnapshotState()
	_, capErr := cap2.Issue(time.Minute)
	after := cap2.SnapshotState()
	check("capacity rejection is ErrCapacity with zero state change",
		errors.Is(capErr, correlate.ErrCapacity) && before == after &&
			cap2.Deliver(x) == nil)

	// 7. Cancel, then late reply treated as orphan.
	canceled := false
	cor.Cancel(newGen)
	if err := cor.Cancel(newGen); errors.Is(err, correlate.ErrNotInFlight) {
		canceled = errors.Is(cor.Deliver(newGen), correlate.ErrOrphanIdle)
	}
	check("cancel frees id; repeat cancel errors; later reply is orphan", canceled)

	// 8. Finished/unknown lookup is zero value and stable across two reads.
	st1, ok1 := cor.Lookup(done.ID)
	st2, ok2 := cor.Lookup(done.ID)
	zero, _ := cor.Lookup(99)
	check("lookup zero value for finished/unknown and identical reads",
		!ok1 && !ok2 && st1 == st2 && zero == correlate.Status{})

	// 9. Same operation sequence gives the same allocation order.
	order := func() []int {
		c := &clock{t: time.Unix(0, 0)}
		q := correlate.New(3, c.now)
		t0, _ := q.Issue(time.Minute)
		q.Issue(time.Minute)
		q.Cancel(t0)
		nxt, _ := q.Issue(time.Minute)
		return []int{t0.ID, nxt.ID}
	}
	r1, r2 := order(), order()
	check("deterministic smallest-free reuse sequence",
		len(r1) == 2 && r1[0] == 0 && r1[1] == 0 && r1[0] == r2[0] && r1[1] == r2[1])

	// 10. Concurrent delivery of one reply: exactly one success.
	concCor := correlate.New(4, clk.now)
	tok, _ := concCor.Issue(time.Minute)
	const n = 32
	var wg sync.WaitGroup
	var wins int64
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if concCor.Deliver(tok) == nil {
				atomic.AddInt64(&wins, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	cs := concCor.SnapshotState()
	check("concurrent same-reply delivery succeeds exactly once",
		atomic.LoadInt64(&wins) == 1 && cs.InFlight == 0 &&
			cs.Completed == 1 && cs.Orphans() == n-1)

	fmt.Printf("TOTAL %d/%d checks OK\n", 10-fails, 10)
}
