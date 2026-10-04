package sched

import (
	"errors"
	"testing"
)

// buildDeepQueue creates one running placeholder and qLen Waiting placeholders
// (distinct groups), filling the queue to exactly qLen.
func buildDeepQueue(t *testing.T, C, qLen int) (*Scheduler, int64, []int64) {
	t.Helper()
	s := mustNew(t, C, qLen)
	runner, err := s.Submit([]byte("grp-runner"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make([]int64, qLen)
	for i := range waiting {
		id, err := s.Submit([]byte("w"+itoa(i)), false, false)
		if err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
		waiting[i] = id
	}
	return s, runner, waiting
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestTouchedBudget proves each operation kind touches at most 4 distinct run
// records, independent of queue length: same assertions at Q=100 and Q=10000.
func TestTouchedBudget(t *testing.T) {
	for _, qLen := range []int{100, 10000} {
		qLen := qLen
		t.Run(queueName(qLen), func(t *testing.T) {
			s, runner, waiting := buildDeepQueue(t, 1, qLen)

			// Submit (new placeholder, rejected while queue is saturated):
			// only the lookup miss path; accepted-id count must not grow.
			s.beginTouch()
			if _, err := s.Submit([]byte("brand-new"), false, false); err == nil {
				t.Fatalf("expected queue full at len %d", qLen)
			}
			if s.touched != 0 {
				t.Fatalf("rejected submit touched %d records", s.touched)
			}

			// Cancel a Waiting run from the middle of the queue: O(1) via its
			// list element; it touches exactly that one run record.
			mid := waiting[qLen/2]
			s.beginTouch()
			if err := s.Cancel(mid); err != nil {
				t.Fatal(err)
			}
			if s.touched != 1 {
				t.Fatalf("middle Waiting cancel touched %d, want 1", s.touched)
			}

			// Finish the running placeholder with no pending: it touches the
			// runner plus exactly one dequeued head = 2.
			s.beginTouch()
			if err := s.Finish(runner, true); err != nil {
				t.Fatal(err)
			}
			if s.touched > 4 {
				t.Fatalf("Finish touched %d, budget 4", s.touched)
			}
			t.Logf("Q=%d Finish touched=%d (runner + %d dequeued head(s))",
				qLen, s.touched, s.touched-1)

			// Submit to a fresh group now: one new record + dequeued heads;
			// with C=1 exactly one head pops, so at most 2 records touched.
			s.beginTouch()
			if _, err := s.Submit([]byte("after-finish"), false, false); err != nil {
				t.Fatal(err)
			}
			if s.touched > 4 {
				t.Fatalf("Submit after finish touched %d, budget 4", s.touched)
			}
		})
	}
}

func queueName(n int) string {
	if n == 100 {
		return "Queue100"
	}
	return "Queue10000"
}

// TestTouchedSubmitWithPending exercises the 4-record Submit path: old pending
// + placeholder + new run + one dequeued head.
func TestTouchedSubmitWithPending(t *testing.T) {
	s := mustNew(t, 2, 10)
	r1, _ := s.Submit([]byte("g"), false, false) // Running
	_, _ = s.Submit(nil, false, false)           // Running, slots full
	r3, _ := s.Submit([]byte("g"), false, false) // Pending
	r4, _ := s.Submit([]byte("x"), false, false) // Waiting head

	// Cancel r1 -> Cancelling (still holds a slot), then ack to vacate and
	// promote r3 behind r4; r4 pops into the freed slot.
	if err := s.Cancel(r1); err != nil {
		t.Fatal(err)
	}
	s.beginTouch()
	if err := s.AckCancel(r1); err != nil {
		t.Fatal(err)
	}
	// r1 + promoted r3 + dequeued r4 = 3 records.
	if s.touched > 4 {
		t.Fatalf("AckCancel touched %d, budget 4", s.touched)
	}
	if st, _ := s.StateOf(r3); st != Waiting {
		t.Fatalf("promoted r3 joins the tail behind r4 and waits: %s", st)
	}
	if st, _ := s.StateOf(r4); st != Running {
		t.Fatalf("r4 was enqueued earlier and must be Running: %s", st)
	}

	// A cancel-in-progress submit to g: old pending (now absent) path aside,
	// bound by placeholder + new run; no allocation occurs while slots full.
	s.beginTouch()
	if _, err := s.Submit([]byte("g"), true, false); err != nil {
		t.Fatal(err)
	}
	if s.touched > 4 {
		t.Fatalf("Submit cancel path touched %d, budget 4", s.touched)
	}
}

// TestPromotionExceedsQ shows pending promotion is not subject to Q: finishing
// a placeholder while the queue already sits at Q promotes the pending run into
// the (possibly over-Q) queue and lets the allocation step drain it.
func TestPromotionExceedsQ(t *testing.T) {
	C, Q := 2, 2
	s := mustNew(t, C, Q)
	r1, _ := s.Submit([]byte("g"), false, false) // Running
	r2, _ := s.Submit(nil, false, false)         // Running (slots full)
	_ = r2
	r3, _ := s.Submit([]byte("a"), false, false) // Waiting
	r4, _ := s.Submit([]byte("b"), false, false) // Waiting -> queue len 2 == Q
	r5, _ := s.Submit([]byte("g"), false, false) // Pending, not queue-charged

	// Finish r2: one free slot pops r3 (queue now [r4]). Finish r1: its
	// promotion of r5 enqueues behind r4 even though Q=2 transiently, then r4
	// and r5 drain into the two free slots.
	if err := s.Finish(r2, true); err != nil { // r3 pops
		t.Fatal(err)
	}
	// Before r1 finishes the queue holds [r4]; promotion appends r5 even
	// though that transiently leaves the queue at capacity, and the allocation
	// step pops r4 (only one slot is free), leaving r5 Waiting at the tail.
	s.beginTouch()
	if err := s.Finish(r1, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.StateOf(r3); st != Running {
		t.Fatalf("r3 %s", st)
	}
	if st, _ := s.StateOf(r4); st != Running {
		t.Fatalf("r4 %s", st)
	}
	if st, _ := s.StateOf(r5); st != Waiting {
		t.Fatalf("promoted r5 joins tail and waits behind r4: %s", st)
	}
	// Promotions may leave the queue at/above Q without error; freeing the
	// remaining slot drains it and r5 ends Running.
	if err := s.Finish(r3, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.StateOf(r5); st != Running {
		t.Fatalf("r5 after drain: %s", st)
	}
	if s.slots.Queued() != 0 {
		t.Fatalf("queue must be empty when a slot is free: len=%d", s.slots.Queued())
	}
}

func TestNewValidation(t *testing.T) {
	cases := [][2]int{{0, 0}, {10001, 0}, {1, -1}, {1, 100001}}
	for _, cq := range cases {
		if _, err := New(cq[0], cq[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d,%d) = %v", cq[0], cq[1], err)
		}
	}
}
