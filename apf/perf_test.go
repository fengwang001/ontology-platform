package apf

import (
	"fmt"
	"testing"
	"time"
)

// The performance requirement: enqueue, dequeue and finish must not cost
// linearly in the number of flows in a level, nor in the number of waiting
// requests. The controller proves this structurally:
//
//   - flows live in a hash map plus an intrusive linked list, so locating or
//     rotating a flow is O(1);
//   - the round-robin cursor is a single list element, advanced O(1) per
//     dequeued request;
//   - timeout eviction uses a binary heap, O(log n) per waiter, with stale
//     entries discarded lazily.
//
// The tests below verify the heap bound empirically through ProbeCount
// (every heap key comparison increments one counter) and show the count is
// independent of the flow count.

func log2ceil(n uint64) uint64 {
	var k uint64
	for p := uint64(1); p < n; p <<= 1 {
		k++
	}
	return k
}

func scalingController(queueLimit int, timeout time.Duration) *Controller {
	cfg := Config{
		TotalSeats: 1,
		Levels: []Level{
			{Name: "L1", Shares: 1, QueueLimit: queueLimit, QueueTimeout: timeout},
		},
		Rules: []Rule{
			{Name: "all", Precedence: 1, Level: "L1", DistinguishBy: ByUser},
		},
	}
	c, err := NewController(t0, cfg)
	if err != nil {
		panic(err)
	}
	return c
}

// TestPerfEnqueueDrainScaling enqueues W waiters across F flows and drains
// them through finishes, asserting the heap comparison count stays within
// O(W log W) regardless of F.
func TestPerfEnqueueDrainScaling(t *testing.T) {
	const W = 20000
	bound := uint64(8) * W * (log2ceil(W) + 1)
	for _, flows := range []int{1, 100, W} {
		flows := flows
		t.Run(fmt.Sprintf("flows-%d", flows), func(t *testing.T) {
			c := scalingController(W+10, time.Duration(100*W)*time.Second)
			tm := t0
			step := func() { tm = tm.Add(time.Second) }
			// One executor occupies the only seat so all others queue.
			res, err := c.Admit(tm, Request{User: "executor", Seats: 1})
			if err != nil {
				t.Fatalf("executor admit: %v", err)
			}
			running := res.Lease
			tickets := make([]*Ticket, 0, W)
			for i := 0; i < W; i++ {
				step()
				res, err := c.Admit(tm, Request{User: fmt.Sprintf("u%d", i%flows), Seats: 1})
				if err != nil {
					t.Fatalf("enqueue %d: %v", i, err)
				}
				tickets = append(tickets, res.Ticket)
			}
			// Drain: each finish dequeues exactly one waiter.
			for i := 0; i < W; i++ {
				step()
				if err := running.Finish(tm); err != nil {
					t.Fatalf("finish %d: %v", i, err)
				}
				r := <-tickets[i].C()
				if r.Err != nil {
					t.Fatalf("ticket %d: %v", i, r.Err)
				}
				running = r.Lease
			}
			step()
			if err := running.Finish(tm); err != nil {
				t.Fatalf("final finish: %v", err)
			}
			probes := c.ProbeCount()
			t.Logf("W=%d flows=%d probes=%d bound=%d (%.1f probes/op)",
				W, flows, probes, bound, float64(probes)/float64(2*W))
			if probes > bound {
				t.Fatalf("probes %d exceed bound %d: cost grows faster than O(W log W)", probes, bound)
			}
		})
	}
}

// TestPerfEvictionScaling enqueues W waiters and evicts all of them with a
// single clock advance, asserting the same O(W log W) bound.
func TestPerfEvictionScaling(t *testing.T) {
	const W = 20000
	timeout := time.Duration(10*W) * time.Second
	c := scalingController(W+10, timeout)
	tm := t0
	res, err := c.Admit(tm, Request{User: "executor", Seats: 1})
	if err != nil {
		t.Fatalf("executor admit: %v", err)
	}
	for i := 0; i < W; i++ {
		tm = tm.Add(time.Second)
		if _, err := c.Admit(tm, Request{User: fmt.Sprintf("u%d", i), Seats: 1}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	// Jump past every deadline: all W waiters are evicted by one operation.
	tm = tm.Add(timeout + time.Second)
	if err := res.Lease.Finish(tm); err != nil {
		t.Fatalf("finish: %v", err)
	}
	probes := c.ProbeCount()
	bound := uint64(8) * W * (log2ceil(W) + 1)
	t.Logf("W=%d eviction probes=%d bound=%d", W, probes, bound)
	if probes > bound {
		t.Fatalf("probes %d exceed bound %d", probes, bound)
	}
	_, levels := c.DebugState()
	if got := levelByName(levels, "L1").Waiters; got != 0 {
		t.Fatalf("waiters = %d, want 0 after mass eviction", got)
	}
}

// BenchmarkAdmitEnqueue measures enqueue cost with a deep queue.
func BenchmarkAdmitEnqueue(b *testing.B) {
	c := scalingController(b.N+10, time.Hour)
	if _, err := c.Admit(t0, Request{User: "executor", Seats: 1}); err != nil {
		b.Fatal(err)
	}
	tm := t0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tm = tm.Add(time.Nanosecond)
		if _, err := c.Admit(tm, Request{User: fmt.Sprintf("u%d", i), Seats: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFinishDispatch measures the finish+dispatch pair on a deep queue.
func BenchmarkFinishDispatch(b *testing.B) {
	c := scalingController(b.N+10, time.Hour)
	res, err := c.Admit(t0, Request{User: "executor", Seats: 1})
	if err != nil {
		b.Fatal(err)
	}
	running := res.Lease
	tickets := make([]*Ticket, 0, b.N)
	tm := t0
	for i := 0; i < b.N; i++ {
		tm = tm.Add(time.Nanosecond)
		r, err := c.Admit(tm, Request{User: fmt.Sprintf("u%d", i), Seats: 1})
		if err != nil {
			b.Fatal(err)
		}
		tickets = append(tickets, r.Ticket)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tm = tm.Add(time.Nanosecond)
		if err := running.Finish(tm); err != nil {
			b.Fatal(err)
		}
		r := <-tickets[i].C()
		running = r.Lease
	}
}
