package schedqueue

import "testing"

func mustNew(t *testing.T, B, M, L int64) *Queue {
	t.Helper()
	q, err := New(B, M, L)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) rejected: %v", B, M, L, err)
	}
	return q
}

func mustReason(t *testing.T, err error, want RejectReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection %v, got nil", want)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if e.Reason != want {
		t.Fatalf("expected reason %v, got %v (%v)", want, e.Reason, err)
	}
}

func mustSizes(t *testing.T, q *Queue, active, backoff, unsched, inFlight int) {
	t.Helper()
	a, b, u, f := q.Sizes()
	if a != active || b != backoff || u != unsched || f != inFlight {
		t.Fatalf("Sizes()=(%d,%d,%d,%d), want (%d,%d,%d,%d)",
			a, b, u, f, active, backoff, unsched, inFlight)
	}
}

func mustPop(t *testing.T, q *Queue, now int64, wantID string, wantAtt int) {
	t.Helper()
	p, ok, err := q.Pop(now)
	if err != nil {
		t.Fatalf("Pop(%d) rejected: %v", now, err)
	}
	if !ok {
		t.Fatalf("Pop(%d) returned no pod, want %q", now, wantID)
	}
	if p.ID != wantID || p.Att != wantAtt {
		t.Fatalf("Pop(%d)=(%q,att=%d), want (%q,att=%d)", now, p.ID, p.Att, wantID, wantAtt)
	}
}

func mustPopEmpty(t *testing.T, q *Queue, now int64) {
	t.Helper()
	_, ok, err := q.Pop(now)
	if err != nil {
		t.Fatalf("Pop(%d) rejected: %v", now, err)
	}
	if ok {
		t.Fatalf("Pop(%d) returned a pod, want empty", now)
	}
}

func mustErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

// checkInvariant verifies the structural invariants of q (same package).
func checkInvariant(t *testing.T, q *Queue) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	a, b, u, f := len(q.active), len(q.backoff), len(q.unsched), len(q.inFlight)
	if a+b+u+f != len(q.pods) {
		t.Fatalf("invariant: sizes %d+%d+%d+%d != live pods %d", a, b, u, f, len(q.pods))
	}
	for i, p := range q.active {
		if p.st != stateActive || p.index != i {
			t.Fatalf("invariant: active[%d]=%q st=%v index=%d", i, p.id, p.st, p.index)
		}
	}
	for i, p := range q.backoff {
		if p.st != stateBackoff || p.index != i {
			t.Fatalf("invariant: backoff[%d]=%q st=%v index=%d", i, p.id, p.st, p.index)
		}
	}
	for id, p := range q.unsched {
		if p.st != stateUnschedulable || p.id != id {
			t.Fatalf("invariant: unsched[%q] st=%v", id, p.st)
		}
	}
	for id, p := range q.inFlight {
		if p.st != stateInFlight || p.id != id {
			t.Fatalf("invariant: inFlight[%q] st=%v", id, p.st)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	for _, cfg := range [][3]int64{
		{0, 10, 10}, {10, 5, 10}, {1, 1e12 + 1, 10}, {1, 10, 0}, {1, 10, 1e12 + 1},
		{-1, 10, 10}, {1, 10, -5},
	} {
		_, err := New(cfg[0], cfg[1], cfg[2])
		if err == nil {
			t.Fatalf("New%v accepted, want RejectInvalidConfig", cfg)
		}
		mustReason(t, err, RejectInvalidConfig)
	}
	if _, err := New(1, 1, 1); err != nil {
		t.Fatalf("New(1,1,1) rejected: %v", err)
	}
	if _, err := New(1e12, 1e12, 1e12); err != nil {
		t.Fatalf("New(1e12,1e12,1e12) rejected: %v", err)
	}
}

// failWithEvent pops id at now, fires a related event, then fails it so it
// lands in the backoff queue with exp = now + d.
func failWithEvent(t *testing.T, q *Queue, id string, now int64, att int) {
	t.Helper()
	mustPop(t, q, now, id, att)
	mustErr(t, q.Event(1, now))
	mustErr(t, q.Done(id, Failed, 1, now))
	mustSizes(t, q, 0, 1, 0, 0)
}

// probeExpiry checks the pod stays in backoff at exp-1 and activates at exp.
func probeExpiry(t *testing.T, q *Queue, now, d int64) {
	t.Helper()
	mustErr(t, q.Advance(now+d-1))
	mustSizes(t, q, 0, 1, 0, 0)
	mustErr(t, q.Advance(now+d))
	mustSizes(t, q, 1, 0, 0, 0)
}

func TestBackoffDoubling(t *testing.T) {
	q := mustNew(t, 1000, 1_000_000_000_000, 60_000)
	mustErr(t, q.Add("p", 1, 0))
	now := int64(0)
	for att, d := range []int64{1000, 2000, 4000} {
		failWithEvent(t, q, "p", now, att+1)
		probeExpiry(t, q, now, d)
		now += d
	}
	checkInvariant(t, q)
}

func TestBackoffCapExactAndOver(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60_000)
	mustErr(t, q.Add("p", 1, 0))
	now := int64(0)
	// att=1..5: 1000, 2000, 4000, 8000 (==M), 16000 capped to 8000.
	for att, d := range []int64{1000, 2000, 4000, 8000, 8000} {
		failWithEvent(t, q, "p", now, att+1)
		probeExpiry(t, q, now, d)
		now += d
	}
	checkInvariant(t, q)
}

func TestBackoffDurationNoOverflow(t *testing.T) {
	for _, tc := range []struct {
		B, M int64
		att  int
		want int64
	}{
		{1000, 8000, 1, 1000},
		{1000, 8000, 4, 8000}, // exactly M
		{1000, 8000, 5, 8000}, // 16000 capped
		{1, 1e12, 100, 1e12},  // huge att capped
		{3, 1e12, 200, 1e12},  // huge att, odd base
		{1e12, 1e12, 1, 1e12}, // B == M
		{1, 1e12, 1, 1},       // att=1 plain
		{7, 1e12, 2, 14},      // att=2 plain
	} {
		if got := backoffDuration(tc.B, tc.M, tc.att); got != tc.want {
			t.Fatalf("backoffDuration(%d,%d,%d)=%d, want %d",
				tc.B, tc.M, tc.att, got, tc.want)
		}
	}
}

// A pod failing repeatedly up to a large att never overflows exp.
func TestHugeAttNoOverflow(t *testing.T) {
	q := mustNew(t, 1, 1e12, 1e12)
	mustErr(t, q.Add("p", 1, 0))
	now := int64(0)
	for att := 1; att <= 60; att++ {
		failWithEvent(t, q, "p", now, att)
		d := backoffDuration(1, 1e12, att)
		probeExpiry(t, q, now, d)
		now += d
	}
	checkInvariant(t, q)
}

// The worked example from the spec: unrelated event before Done parks the
// pod; a related event then moves it to backoff since now < exp.
func TestSpecExample(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1)
	mustErr(t, q.Event(2, 100))
	mustErr(t, q.Done("p", Failed, 4, 200)) // 2&4==0 -> unschedulable, exp=1200
	mustSizes(t, q, 0, 0, 1, 0)
	mustErr(t, q.Event(4, 300)) // related, 300<1200 -> backoff
	mustSizes(t, q, 0, 1, 0, 0)
	mustErr(t, q.Advance(1199))
	mustSizes(t, q, 0, 1, 0, 0)
	mustErr(t, q.Advance(1200))
	mustSizes(t, q, 1, 0, 0, 0)
	checkInvariant(t, q)
}

// Same setup, but fb=2 matches the event: Done goes straight to backoff.
func TestSpecExampleDirectBackoff(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1)
	mustErr(t, q.Event(2, 100))
	mustErr(t, q.Done("p", Failed, 2, 200)) // 2&2!=0 -> backoff, exp=1200
	mustSizes(t, q, 0, 1, 0, 0)
	mustErr(t, q.Advance(1200))
	mustSizes(t, q, 1, 0, 0, 0)
	checkInvariant(t, q)
}

// Events before Pop do not count for that flight.
func TestEventBeforePopIgnored(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Event(1, 0)) // seq=1 before the pod exists
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1) // records seq=1
	mustErr(t, q.Done("p", Failed, 1, 0))
	mustSizes(t, q, 0, 0, 1, 0) // no event after seq=1 -> unschedulable
	checkInvariant(t, q)
}

// fb=0 is related to every event.
func TestFbZeroRelatedToAnyEvent(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1)
	mustErr(t, q.Event(128, 10))
	mustErr(t, q.Done("p", Failed, 0, 20))
	mustSizes(t, q, 0, 1, 0, 0) // related -> backoff
	checkInvariant(t, q)

	// Parked with fb=0: any event moves it.
	q2 := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q2.Add("p", 1, 0))
	mustPop(t, q2, 0, "p", 1)
	mustErr(t, q2.Done("p", Failed, 0, 0)) // no event -> unschedulable
	mustSizes(t, q2, 0, 0, 1, 0)
	mustErr(t, q2.Event(255, 5)) // 5 < exp=1000 -> backoff
	mustSizes(t, q2, 0, 1, 0, 0)
	checkInvariant(t, q2)
}

// An unrelated event leaves the pod in the unschedulable queue.
func TestUnrelatedEventStaysParked(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1)
	mustErr(t, q.Done("p", Failed, 4, 0)) // exp=1000, parked=0
	mustSizes(t, q, 0, 0, 1, 0)
	mustErr(t, q.Event(2, 10))  // 4&2==0
	mustErr(t, q.Event(1, 20))  // 4&1==0
	mustErr(t, q.Event(16, 30)) // 4&16==0
	mustSizes(t, q, 0, 0, 1, 0)
	mustErr(t, q.Event(5, 40)) // 4&5!=0, 40<1000 -> backoff
	mustSizes(t, q, 0, 1, 0, 0)
	checkInvariant(t, q)
}

// Related event with now >= exp moves the pod to active, not backoff.
func TestRelatedEventAfterExpiryActivates(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1)
	mustErr(t, q.Done("p", Failed, 4, 0)) // exp=1000
	mustErr(t, q.Event(4, 999))
	mustSizes(t, q, 0, 1, 0, 0) // 999 < 1000 -> backoff
	checkInvariant(t, q)

	q2 := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q2.Add("p", 1, 0))
	mustPop(t, q2, 0, "p", 1)
	mustErr(t, q2.Done("p", Failed, 4, 0)) // exp=1000
	mustErr(t, q2.Event(4, 1000))          // now == exp -> active
	mustSizes(t, q2, 1, 0, 0, 0)
	checkInvariant(t, q2)
}

// parked+L == now moves the pod out of unschedulable, related or not;
// destination depends on now < exp.
func TestParkedTTLBoundary(t *testing.T) {
	// now >= exp at TTL expiry -> active.
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("p", 1, 0))
	mustPop(t, q, 0, "p", 1)
	mustErr(t, q.Done("p", Failed, 4, 100)) // parked=100, exp=1100
	mustErr(t, q.Advance(100+60000-1))
	mustSizes(t, q, 0, 0, 1, 0)
	mustErr(t, q.Advance(100+60000)) // 60100 >= 1100 -> active
	mustSizes(t, q, 1, 0, 0, 0)
	checkInvariant(t, q)

	// now < exp at TTL expiry -> backoff.
	q2 := mustNew(t, 1_000_000, 1_000_000_000_000, 10)
	mustErr(t, q2.Add("p", 1, 0))
	mustPop(t, q2, 0, "p", 1)
	mustErr(t, q2.Done("p", Failed, 4, 0)) // parked=0, exp=1e6
	mustErr(t, q2.Advance(9))
	mustSizes(t, q2, 0, 0, 1, 0)
	mustErr(t, q2.Advance(10)) // 10 < 1e6 -> backoff
	mustSizes(t, q2, 0, 1, 0, 0)
	checkInvariant(t, q2)
}

// Pop order: prio desc, then t0 asc, then id bytewise asc.
func TestPopPriorityTieBreak(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("e", 1, 0)) // lowest prio
	mustErr(t, q.Add("c", 5, 5)) // same prio, earlier t0
	mustErr(t, q.Add("b", 5, 10))
	mustErr(t, q.Add("a", 5, 10)) // same prio+t0 as b, smaller id
	mustErr(t, q.Add("d", 9, 50)) // highest prio
	mustPop(t, q, 50, "d", 1)
	mustPop(t, q, 50, "c", 1)
	mustPop(t, q, 50, "a", 1)
	mustPop(t, q, 50, "b", 1)
	mustPop(t, q, 50, "e", 1)
	mustPopEmpty(t, q, 50)
	checkInvariant(t, q)
}

// A retried pod keeps its original t0 when competing with newer pods.
func TestRetryKeepsOriginalT0(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("old", 5, 0))
	mustPop(t, q, 0, "old", 1)
	mustErr(t, q.Event(1, 0))
	mustErr(t, q.Done("old", Failed, 1, 0)) // backoff, exp=1000
	mustErr(t, q.Add("new", 5, 500))
	mustErr(t, q.Advance(1000)) // old -> active with t0=0
	mustSizes(t, q, 2, 0, 0, 0)
	mustPop(t, q, 1000, "old", 2) // t0=0 beats t0=500
	mustPop(t, q, 1000, "new", 1)
	checkInvariant(t, q)
}

// Remove works from every location; Done after Remove is rejected.
func TestRemove(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("a", 1, 0))
	mustErr(t, q.Add("b", 2, 0))
	mustErr(t, q.Add("c", 3, 0))
	mustErr(t, q.Add("d", 4, 0))
	// in flight
	mustPop(t, q, 0, "d", 1)
	mustErr(t, q.Remove("d"))
	mustSizes(t, q, 3, 0, 0, 0)
	mustReason(t, q.Done("d", Scheduled, 0, 0), RejectPodNotFound)
	// backoff
	mustPop(t, q, 0, "c", 1)
	mustErr(t, q.Event(1, 0))
	mustErr(t, q.Done("c", Failed, 1, 0))
	mustSizes(t, q, 2, 1, 0, 0)
	mustErr(t, q.Remove("c"))
	mustSizes(t, q, 2, 0, 0, 0)
	// unschedulable
	mustPop(t, q, 0, "b", 1)
	mustErr(t, q.Done("b", Failed, 1, 0))
	mustSizes(t, q, 1, 0, 1, 0)
	mustErr(t, q.Remove("b"))
	mustSizes(t, q, 1, 0, 0, 0)
	// active
	mustErr(t, q.Remove("a"))
	mustSizes(t, q, 0, 0, 0, 0)
	mustReason(t, q.Remove("a"), RejectPodNotFound)
	checkInvariant(t, q)
}

// Done on a live but not in-flight pod is rejected distinctly.
func TestDoneNotInFlight(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("a", 1, 0))
	mustReason(t, q.Done("a", Failed, 1, 0), RejectPodNotInFlight)
	mustReason(t, q.Done("ghost", Failed, 1, 0), RejectPodNotFound)
	mustPop(t, q, 0, "a", 1)
	mustErr(t, q.Done("a", Failed, 1, 0)) // parks in unschedulable
	mustReason(t, q.Done("a", Failed, 1, 0), RejectPodNotInFlight)
	checkInvariant(t, q)
}

// Scheduled removes the pod and ignores fb.
func TestDoneScheduled(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("a", 1, 0))
	mustPop(t, q, 0, "a", 1)
	mustErr(t, q.Done("a", Scheduled, 7, 0))
	mustSizes(t, q, 0, 0, 0, 0)
	mustReason(t, q.Done("a", Scheduled, 0, 0), RejectPodNotFound)
	checkInvariant(t, q)
}

// Rejection reasons follow the mandated order and rejected ops change
// nothing: queues, att, fb and counters are untouched.
func TestRejectionsOrderedAndSideEffectFree(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("a", 1, 100))

	// Invalid argument beats clock regression.
	mustReason(t, q.Add("", 1, 50), RejectInvalidArgument)
	mustReason(t, q.Add("x", 1, -1), RejectInvalidArgument)
	mustReason(t, q.Event(0, 50), RejectInvalidArgument)
	mustReason(t, q.Event(256, 50), RejectInvalidArgument)
	mustReason(t, q.Done("a", Outcome(9), 1, 50), RejectInvalidArgument)
	mustReason(t, q.Done("a", Failed, 256, 50), RejectInvalidArgument)
	mustReason(t, q.Done("a", Failed, -1, 50), RejectInvalidArgument)
	mustReason(t, q.Advance(-1), RejectInvalidArgument)
	mustReason(t, q.Remove(""), RejectInvalidArgument)

	// Clock regression beats duplicate / not-found / not-in-flight.
	mustReason(t, q.Add("a", 1, 50), RejectClockRegression)
	_, _, popErr := q.Pop(50)
	mustReason(t, popErr, RejectClockRegression)
	mustReason(t, q.Event(1, 50), RejectClockRegression)
	mustReason(t, q.Advance(50), RejectClockRegression)
	mustReason(t, q.Done("a", Failed, 1, 50), RejectClockRegression)

	// State-based rejections at a valid clock.
	mustReason(t, q.Add("a", 2, 100), RejectDuplicateID)
	mustReason(t, q.Done("a", Failed, 1, 100), RejectPodNotInFlight)
	mustReason(t, q.Done("ghost", Failed, 1, 100), RejectPodNotFound)
	mustReason(t, q.Remove("ghost"), RejectPodNotFound)

	// Nothing changed: one active pod with att=0, no events recorded.
	mustSizes(t, q, 1, 0, 0, 0)
	mustPop(t, q, 100, "a", 1)
	mustErr(t, q.Done("a", Failed, 1, 100)) // no related event -> parked
	mustSizes(t, q, 0, 0, 1, 0)
	checkInvariant(t, q)
}

// A rejected op does not raise the max-now watermark.
func TestRejectedOpDoesNotAdvanceClock(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustErr(t, q.Add("a", 1, 100))
	mustReason(t, q.Add("b", 1, 50), RejectClockRegression)
	mustErr(t, q.Add("b", 1, 100)) // maxNow still 100
	mustSizes(t, q, 2, 0, 0, 0)
}

// Pop on an empty active queue succeeds with no pod and still counts for
// the clock watermark.
func TestPopEmptyCountsForClock(t *testing.T) {
	q := mustNew(t, 1000, 8000, 60000)
	mustPopEmpty(t, q, 500)
	mustReason(t, q.Add("a", 1, 499), RejectClockRegression)
	mustErr(t, q.Add("a", 1, 500))
	mustPop(t, q, 500, "a", 1)
}
