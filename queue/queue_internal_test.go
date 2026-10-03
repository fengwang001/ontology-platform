package queue

import (
	"errors"
	"fmt"
	"testing"
)

// TestExaminationCounters verifies that planning an eviction examines at most
// evicted+1 records, and draining examines at most returned+1 records, at the
// 100 and 10000 record scales.
func TestExaminationCounters(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			q := New(int64(n))
			// Fill with n size-1 records, alternating lanes 1 and 2.
			for i := 0; i < n; i++ {
				prio := 1 + i%2
				if _, _, err := q.Admit("T", prio, 1); err != nil {
					t.Fatalf("fill %d: %v", i, err)
				}
			}
			if q.Used() != int64(n) {
				t.Fatalf("used = %d, want %d", q.Used(), n)
			}
			// Admit a size-2 prio-0 record: need 2 bytes; the two newest
			// lane-2 records are examined and evicted (shortest prefix).
			q.evictExamined, q.evictReturned = 0, 0
			if _, evicted, err := q.Admit("T", 0, 2); err != nil {
				t.Fatalf("admit: %v", err)
			} else if len(evicted) != 2 {
				t.Fatalf("evicted = %d, want 2", len(evicted))
			}
			if q.evictExamined > q.evictReturned+1 {
				t.Fatalf("examined %d > evicted+1 %d", q.evictExamined, q.evictReturned+1)
			}

			// Drain with budget 4: takes the size-2 prio-0 record and two
			// size-1 prio-1 records, then the next size-1 record does not
			// fit, so examined == returned+1.
			q.drainExamined, q.drainReturned = 0, 0
			budget := int64(4)
			out := q.PopDrain(budget)
			var bytes int64
			for _, r := range out {
				bytes += r.Size
			}
			if bytes != budget || len(out) != 3 {
				t.Fatalf("drained %d records / %d bytes, want 3 records / 4 bytes",
					len(out), bytes)
			}
			if q.drainExamined > q.drainReturned+1 {
				t.Fatalf("drain examined %d > returned+1 %d",
					q.drainExamined, q.drainReturned+1)
			}
		})
	}
}

// TestEvictionOrder checks lowest-priority-first, newest-within-lane, the
// shortest-prefix stop rule, and that same-priority records are never evicted.
func TestEvictionOrder(t *testing.T) {
	q := New(10)
	mustAdmit := func(tenant string, prio int, size int64, wantSeq int64) {
		t.Helper()
		r, _, err := q.Admit(tenant, prio, size)
		if err != nil {
			t.Fatalf("admit %s p%d s%d: %v", tenant, prio, size, err)
		}
		if r.Seq != wantSeq {
			t.Fatalf("seq = %d, want %d", r.Seq, wantSeq)
		}
	}
	mustAdmit("a", 1, 3, 1)
	mustAdmit("b", 2, 2, 2)
	mustAdmit("c", 2, 2, 3)
	mustAdmit("d", 2, 2, 4)
	mustAdmit("e", 1, 1, 5)
	if q.Used() != 10 {
		t.Fatalf("used = %d, want 10", q.Used())
	}
	// Prio-0 size-4: need 4; lane 2 has 6 bytes (seq 2,3,4). Evict newest
	// first: seq4 (2 bytes), then seq3 (2 bytes) -> stop. Seq2 and lane 1
	// survive.
	plan, err := q.PlanAdmit(0, 4)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Evict) != 2 || plan.Evict[0].Seq != 4 || plan.Evict[1].Seq != 3 {
		t.Fatalf("eviction plan = %+v", plan.Evict)
	}
	if got := q.EvictableBytes(1); got != 6 {
		t.Fatalf("evictable for prio 1 = %d, want 6", got)
	}

	// Same-priority admission cannot evict lane-1 records: prio-1 size-2 has
	// only lane 2 available (need 2, available 6) and must pop only seq4.
	q2 := New(10)
	q2.Admit("a", 1, 3)
	q2.Admit("b", 2, 2)
	q2.Admit("c", 2, 2)
	q2.Admit("d", 1, 3)
	plan2, err := q2.PlanAdmit(1, 2)
	if err != nil {
		t.Fatalf("plan2: %v", err)
	}
	if len(plan2.Evict) != 1 || plan2.Evict[0].Prio != 2 {
		t.Fatalf("plan2 = %+v", plan2.Evict)
	}

	// Evictable bytes insufficient: no state change and ErrQueueFull.
	before := q.Snapshot()
	_, err = q.PlanAdmit(0, 11)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	after := q.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("queue mutated after failed plan: %d -> %d", len(before), len(after))
	}
	if q.Used() != 10 {
		t.Fatalf("used after failed plan = %d", q.Used())
	}
}

// TestDrainStopsAtFirstOversize verifies no skipping past a big record.
func TestDrainStopsAtFirstOversize(t *testing.T) {
	q := New(100)
	q.Admit("a", 0, 6)
	q.Admit("b", 1, 5)
	q.Admit("c", 1, 1)
	out := q.PopDrain(10)
	// Takes prio-0 6 bytes; next prio-1 5 bytes exceeds remaining 4, so the
	// later size-1 record must not be taken.
	if len(out) != 1 || out[0].Seq != 1 {
		t.Fatalf("drain = %+v", out)
	}
	if q.Used() != 6 {
		t.Fatalf("used = %d, want 6", q.Used())
	}
}

// TestInvariantsAfterMixedOps checks used == sum of records and lane bytes.
func TestInvariantsAfterMixedOps(t *testing.T) {
	q := New(50)
	sizes := []int64{7, 3, 9, 2, 11, 4, 8, 6}
	for i, s := range sizes {
		q.Admit("t", i%3, s)
	}
	var sum, laneSum int64
	for _, r := range q.Snapshot() {
		sum += r.Size
	}
	for p := 0; p < Priorities; p++ {
		laneSum += q.UsedInLane(p)
	}
	if sum != q.Used() || laneSum != q.Used() || sum > q.Cap() {
		t.Fatalf("invariants: sum=%d lanes=%d used=%d cap=%d", sum, laneSum, q.Used(), q.Cap())
	}
}
