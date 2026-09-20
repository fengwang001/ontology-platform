// Command demo exercises every documented semantic of the drain gate
// and prints one OK/FAIL line per semantic. It always exits 0.
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/drain"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

var failures int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func waitDraining(g *drain.Gate) {
	for {
		r, err := g.Enter()
		if err != nil {
			return
		}
		r()
	}
}

func main() {
	// 1. New requests are rejected after shutdown starts.
	g1 := drain.New(nil)
	hold1, _ := g1.Enter()
	done1 := make(chan error, 1)
	go func() { done1 <- g1.Shutdown(time.Now().Add(time.Hour)) }()
	waitDraining(g1)
	before := g1.Stats()
	r, err := g1.Enter()
	ok1 := errors.Is(err, drain.ErrShuttingDown) && r == nil &&
		g1.Stats().Rejected == before.Rejected+1 && g1.Stats().Admitted == before.Admitted
	hold1()
	<-done1
	check("1 reject-after-shutdown (nil release, Rejected+1)", ok1)

	// 2+6. Shutdown waits for in-flight requests; releases during the
	// wait decrement InFlight and wake Shutdown the moment it hits zero.
	g2 := drain.New(nil)
	rs := make([]func(), 3)
	for i := range rs {
		rs[i], _ = g2.Enter()
	}
	done2 := make(chan error, 1)
	go func() { done2 <- g2.Shutdown(time.Now().Add(time.Hour)) }()
	waitDraining(g2)
	rs[0]()
	rs[1]()
	blocked := false
	select {
	case <-done2:
	case <-time.After(50 * time.Millisecond):
		blocked = true
	}
	decremented := g2.Stats().InFlight == 1
	rs[2]()
	prompt := false
	select {
	case err := <-done2:
		prompt = err == nil
	case <-time.After(time.Second):
	}
	check("2 waits-for-inflight, nil immediately at zero", blocked && prompt)
	check("6 releases during drain decrement and wake", decremented)

	// 3+7. Timeout: ErrDrainTimeout, InFlight kept, Done set; late
	// releases still count down to zero.
	c3 := &clock{now: time.Unix(1000, 0)}
	g3 := drain.New(c3.Now)
	l1, _ := g3.Enter()
	l2, _ := g3.Enter()
	done3 := make(chan error, 1)
	go func() { done3 <- g3.Shutdown(c3.Now().Add(10 * time.Second)) }()
	waitDraining(g3)
	c3.Add(time.Minute)
	g3.Tick()
	err = <-done3
	s3 := g3.Stats()
	check("3 timeout (ErrDrainTimeout, InFlight=2, Done)", errors.Is(err, drain.ErrDrainTimeout) && s3.InFlight == 2 && s3.Done)
	l1()
	l2()
	check("7 late releases after timeout reach zero", g3.Stats().InFlight == 0)

	// 4. A release is idempotent, even under concurrency, never negative.
	g4 := drain.New(nil)
	rel, _ := g4.Enter()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); rel() }()
	}
	wg.Wait()
	rel()
	check("4 release exactly-once, InFlight never negative", g4.Stats().InFlight == 0)

	// 5. Concurrent Shutdown calls share one wait and one result.
	c5 := &clock{now: time.Unix(1000, 0)}
	g5 := drain.New(c5.Now)
	hold5, _ := g5.Enter()
	res := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func(i int) { res <- g5.Shutdown(c5.Now().Add(time.Duration(i+1) * time.Minute)) }(i)
	}
	waitDraining(g5)
	admitted5 := g5.Stats().Admitted
	c5.Add(time.Hour)
	g5.Tick()
	same := true
	for i := 0; i < 4; i++ {
		same = same && errors.Is(<-res, drain.ErrDrainTimeout)
	}
	same = same && errors.Is(g5.Shutdown(c5.Now()), drain.ErrDrainTimeout)
	hold5()
	check("5 idempotent shutdown (same result, no recount)", same && g5.Stats().Admitted == admitted5)

	// 8. Concurrency: counter conservation.
	g8 := drain.New(nil)
	var admitted, released, attempts atomic.Int64
	var wg8 sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg8.Add(1)
		go func() {
			defer wg8.Done()
			for i := 0; i < 100; i++ {
				rel, err := g8.Enter()
				attempts.Add(1)
				if err != nil {
					continue
				}
				admitted.Add(1)
				rel()
				released.Add(1)
			}
		}()
	}
	time.AfterFunc(2*time.Millisecond, func() { _ = g8.Shutdown(time.Now().Add(10 * time.Second)) })
	wg8.Wait()
	_ = g8.Shutdown(time.Now())
	s8 := g8.Stats()
	ok8 := int64(s8.Admitted) == admitted.Load() &&
		int64(s8.Admitted+s8.Rejected) == attempts.Load() &&
		int64(s8.InFlight) == admitted.Load()-released.Load() &&
		s8.InFlight == 0 && s8.Done
	check("8 concurrent conservation (Admitted/Rejected/InFlight)", ok8)

	fmt.Printf("summary: %d failed\n", failures)
}
