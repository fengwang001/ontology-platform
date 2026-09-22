package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/correlate"
	"ontology/slot"
)

// manualClock is an injected, concurrency-safe clock measured in nanos.
type manualClock struct{ nanos atomic.Int64 }

func (m *manualClock) now() time.Time { return time.Unix(0, m.nanos.Load()) }

func (m *manualClock) add(d time.Duration) { m.nanos.Add(int64(d)) }

func main() {
	r := &reporter{}

	// Normal association and out-of-order delivery.
	{
		c, _ := demoCorrelator(4)
		a, _ := c.Request(10 * time.Second)
		b, _ := c.Request(10 * time.Second)
		r.check("normal association completes once", c.Deliver(a) == nil && c.InFlight() == 1)
		r.check("out-of-order response matched", c.Deliver(b) == nil && c.InFlight() == 0)
	}

	// Headline sample: late response never completes the new generation.
	{
		c, clk := demoCorrelator(1)
		first, _ := c.Request(10 * time.Second)
		clk.add(10 * time.Second)
		_ = c.Cancel(slot.Encode(42, 1)) // lazy timeout cleanup, no state change
		clk.add(1 * time.Second)
		second, _ := c.Request(10 * time.Second)
		clk.add(1 * time.Second)
		late := c.Deliver(first)
		r.check("late response orphaned, new request in flight",
			errors.Is(late, correlate.ErrStaleID) && c.InFlight() == 1 && c.Deliver(second) == nil)
	}

	// now == deadline counts as timeout (left-closed, right-open).
	{
		c, clk := demoCorrelator(1)
		id, _ := c.Request(10 * time.Second)
		clk.add(10 * time.Second)
		r.check("deadline instant means timed out", errors.Is(c.Deliver(id), correlate.ErrIdleID))
	}

	// The three orphan kinds are pairwise distinguishable and counted.
	{
		c, _ := demoCorrelator(2)
		id, _ := c.Request(time.Minute)
		_ = c.Deliver(id)
		idle := c.Deliver(id)
		unknown := c.Deliver(slot.Encode(123, 9))
		r.check("unknown/idle/stale orphans distinguishable",
			errors.Is(idle, correlate.ErrIdleID) &&
				errors.Is(unknown, correlate.ErrUnknownID) &&
				errors.Is(staleOrphan(c), correlate.ErrStaleID))
	}

	// Capacity rejection must not mutate any state.
	{
		c, _ := demoCorrelator(2)
		c.Request(time.Minute)
		c.Request(time.Minute)
		before := c.Counters()
		_, err := c.Request(time.Minute)
		r.check("capacity refused with zero state change",
			errors.Is(err, correlate.ErrCapacity) && c.InFlight() == 2 && c.Counters() == before)
	}

	// Canceled request: its response is an orphan and double-cancel is typed.
	{
		c, _ := demoCorrelator(2)
		id, _ := c.Request(time.Minute)
		_ = c.Cancel(id)
		r.check("canceled request response is idle orphan",
			errors.Is(c.Deliver(id), correlate.ErrIdleID) &&
				errors.Is(c.Cancel(id), correlate.ErrNotInFlight))
	}

	// Settled lookup is zero; repeated reads are identical.
	{
		c, _ := demoCorrelator(2)
		id, _ := c.Request(time.Minute)
		first := c.Lookup(id)
		_ = c.Deliver(id)
		r.check("settled lookup zero and reads stable",
			c.Lookup(id) == (correlate.Info{}) && first.InFlight && first.Remaining == time.Minute)
	}

	// Deterministic, reproducible allocation order (lowest free index).
	{
		seq := allocationSeq()
		r.check("id allocation sequence reproducible",
			len(seq) == 5 && seq[0] == 0 && seq[2] == 2 && seq[3] == 0 && seq[4] == 2)
	}

	// Concurrent duplicate delivery: exactly one success.
	{
		c, _ := demoCorrelator(1)
		id, _ := c.Request(time.Minute)
		var wins, idle int64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				switch err := c.Deliver(id); {
				case err == nil:
					atomic.AddInt64(&wins, 1)
				case errors.Is(err, correlate.ErrIdleID):
					atomic.AddInt64(&idle, 1)
				}
			}()
		}
		close(start)
		wg.Wait()
		r.check("concurrent same response: exactly one success", wins == 1 && idle == 31)
	}

	fmt.Println(r.total())
}

func demoCorrelator(capacity int) (*correlate.Correlator, *manualClock) {
	clk := &manualClock{}
	return correlate.New(capacity, clk.now), clk
}

func staleOrphan(c *correlate.Correlator) error {
	old, _ := c.Request(time.Minute)
	_ = c.Cancel(old)
	fresh, _ := c.Request(time.Minute)
	if old.Index() != fresh.Index() || old.Generation() == fresh.Generation() {
		return errors.New("setup: slot was not recycled")
	}
	return c.Deliver(old)
}

func allocationSeq() []uint32 {
	c, _ := demoCorrelator(3)
	var seq []uint32
	for range 3 {
		id, _ := c.Request(time.Minute)
		seq = append(seq, id.Index())
	}
	_ = c.Deliver(slot.Encode(2, 1))
	_ = c.Deliver(slot.Encode(0, 1))
	id, _ := c.Request(time.Minute)
	seq = append(seq, id.Index())
	id, _ = c.Request(time.Minute)
	seq = append(seq, id.Index())
	return seq
}
