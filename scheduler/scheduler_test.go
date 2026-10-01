package scheduler

import "testing"

func mustNewQueue(t *testing.T, base, max, retention int64) *SchedulingQueue {
	t.Helper()
	q, err := NewSchedulingQueue(base, max, retention)
	if err != nil {
		t.Fatalf("NewSchedulingQueue(%d, %d, %d): %v", base, max, retention, err)
	}
	return q
}

func TestInvalidConfig(t *testing.T) {
	tests := []struct {
		name      string
		base      int64
		max       int64
		retention int64
	}{
		{"zero base", 0, 1, 1},
		{"max below base", 2, 1, 1},
		{"base over limit", 1_000_000_000_001, 1_000_000_000_001, 1},
		{"retention zero", 1, 1, 0},
		{"retention over limit", 1, 1, 1_000_000_000_001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewSchedulingQueue(tt.base, tt.max, tt.retention); err != ErrInvalidConfig {
				t.Fatalf("error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestQueueConstructsAtBoundary(t *testing.T) {
	mustNewQueue(t, 1, 1_000_000_000_000, 1_000_000_000_000)
}

func popAndFail(t *testing.T, q *SchedulingQueue, id string, failureBits, now int64) {
	t.Helper()
	_, ok, err := q.Pop(now)
	if err != nil || !ok {
		t.Fatalf("Pop(%d) = pod,%v,%v; want pod", now, ok, err)
	}
	if failureBits == 0 {
		if err := q.Event(1, now); err != nil {
			t.Fatalf("Event before Done: %v", err)
		}
	}
	if err := q.Done(id, OutcomeFailed, failureBits, now); err != nil {
		t.Fatalf("Done(%s, failed): %v", id, err)
	}
}

func TestBackoffDoublingAndCap(t *testing.T) {
	q := mustNewQueue(t, 1000, 8000, 60_000)
	if err := q.Add("p", 0, 0); err != nil {
		t.Fatal(err)
	}

	popAndFail(t, q, "p", 0, 0)
	if got := q.pods["p"].expiresAt; got != 1000 {
		t.Fatalf("attempt 1 expiry = %d, want 1000", got)
	}

	if err := q.Advance(1000); err != nil {
		t.Fatal(err)
	}
	popAndFail(t, q, "p", 0, 2000)
	if got := q.pods["p"].expiresAt; got != 4000 {
		t.Fatalf("attempt 2 expiry = %d, want 4000", got)
	}

	if err := q.Advance(4000); err != nil {
		t.Fatal(err)
	}
	popAndFail(t, q, "p", 0, 5000)
	if got := q.pods["p"].expiresAt; got != 9000 {
		t.Fatalf("attempt 3 expiry = %d, want 9000", got)
	}
}

func failAttemptAfterEvent(t *testing.T, q *SchedulingQueue, id string, now int64) {
	t.Helper()
	if _, ok, err := q.Pop(now); err != nil || !ok {
		t.Fatalf("Pop(%d) = %v,%v", now, ok, err)
	}
	if err := q.Event(1, now); err != nil {
		t.Fatal(err)
	}
	if err := q.Done(id, OutcomeFailed, 0, now); err != nil {
		t.Fatal(err)
	}
}

func TestBackoffCapAtAndAboveMax(t *testing.T) {
	q := mustNewQueue(t, 4, 8, 60)
	if err := q.Add("equal", 0, 0); err != nil {
		t.Fatal(err)
	}
	failAttemptAfterEvent(t, q, "equal", 0)
	if err := q.Advance(q.pods["equal"].expiresAt); err != nil {
		t.Fatal(err)
	}
	failAttemptAfterEvent(t, q, "equal", 20)
	if got := q.pods["equal"].expiresAt; got != 28 {
		t.Fatalf("equal-to-max expiry = %d, want 28", got)
	}

	q2 := mustNewQueue(t, 3, 8, 60)
	if err := q2.Add("over", 0, 0); err != nil {
		t.Fatal(err)
	}
	failAttemptAfterEvent(t, q2, "over", 0)
	if err := q2.Advance(q2.pods["over"].expiresAt); err != nil {
		t.Fatal(err)
	}
	failAttemptAfterEvent(t, q2, "over", 20)
	if err := q2.Advance(q2.pods["over"].expiresAt); err != nil {
		t.Fatal(err)
	}
	failAttemptAfterEvent(t, q2, "over", 40)
	if got := q2.pods["over"].expiresAt; got != 48 {
		t.Fatalf("over-max expiry = %d, want 48", got)
	}
}

func TestBackoffLargeAttemptDoesNotOverflow(t *testing.T) {
	q := mustNewQueue(t, 1, 1_000_000_000_000, 60)
	entry := &pod{attempts: 70}
	if got := q.backoffDeadlineLocked(entry, 100); got != 1_000_000_000_100 {
		t.Fatalf("large-attempt expiry = %d, want capped value", got)
	}
}

func stateOf(q *SchedulingQueue, id string) podState {
	return q.pods[id].state
}

func TestBackoffExpiryBoundary(t *testing.T) {
	q := mustNewQueue(t, 10, 100, 100)
	if err := q.Add("p", 0, 0); err != nil {
		t.Fatal(err)
	}

	if _, _, err := q.Pop(0); err != nil {
		t.Fatal(err)
	}
	if err := q.Event(1, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Done("p", OutcomeFailed, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "p"); got != stateBackoff {
		t.Fatalf("state after Done = %d, want backoff", got)
	}

	if err := q.Advance(9); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "p"); got != stateBackoff {
		t.Fatalf("state one millisecond before expiry = %d, want backoff", got)
	}

	if err := q.Advance(10); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "p"); got != stateActive {
		t.Fatalf("state at expiry = %d, want active", got)
	}
}

func TestParkedRetentionMovesRelatedAndUnrelated(t *testing.T) {
	q := mustNewQueue(t, 10, 100, 5)
	if err := q.Add("related", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("unrelated", 0, 0); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"related", "unrelated"} {
		if _, _, err := q.Pop(0); err != nil {
			t.Fatal(err)
		}
		if err := q.Done(id, OutcomeFailed, 4, 0); err != nil {
			t.Fatal(err)
		}
	}

	if err := q.Event(2, 1); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "related"); got != stateUnschedulable {
		t.Fatalf("related state after non-matching event = %d, want parked", got)
	}
	if got := stateOf(q, "unrelated"); got != stateUnschedulable {
		t.Fatalf("unrelated state after non-matching event = %d, want parked", got)
	}

	if err := q.Advance(4); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "related"); got != stateUnschedulable {
		t.Fatalf("state one before retention = %d, want parked", got)
	}

	if err := q.Advance(5); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "related"); got != stateBackoff {
		t.Fatalf("related state at retention with future exp = %d, want backoff", got)
	}
	if got := stateOf(q, "unrelated"); got != stateBackoff {
		t.Fatalf("unrelated state at retention = %d, want backoff", got)
	}
}

func TestInFlightEventRelevance(t *testing.T) {
	t.Run("event before pop does not affect flight", func(t *testing.T) {
		q := mustNewQueue(t, 10, 100, 100)
		if err := q.Add("p", 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := q.Event(2, 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.Pop(1); err != nil {
			t.Fatal(err)
		}
		if err := q.Done("p", OutcomeFailed, 2, 1); err != nil {
			t.Fatal(err)
		}
		if got := stateOf(q, "p"); got != stateUnschedulable {
			t.Fatalf("state = %d, want unschedulable", got)
		}
	})

	t.Run("related event routes to backoff", func(t *testing.T) {
		q := mustNewQueue(t, 10, 100, 100)
		if err := q.Add("p", 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.Pop(0); err != nil {
			t.Fatal(err)
		}
		if err := q.Event(2, 0); err != nil {
			t.Fatal(err)
		}
		if err := q.Done("p", OutcomeFailed, 4|2, 0); err != nil {
			t.Fatal(err)
		}
		if got := stateOf(q, "p"); got != stateBackoff {
			t.Fatalf("state = %d, want backoff", got)
		}
	})

	t.Run("unrelated event routes to unschedulable", func(t *testing.T) {
		q := mustNewQueue(t, 10, 100, 100)
		if err := q.Add("p", 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.Pop(0); err != nil {
			t.Fatal(err)
		}
		if err := q.Event(2, 0); err != nil {
			t.Fatal(err)
		}
		if err := q.Done("p", OutcomeFailed, 4, 0); err != nil {
			t.Fatal(err)
		}
		if got := stateOf(q, "p"); got != stateUnschedulable {
			t.Fatalf("state = %d, want unschedulable", got)
		}
	})

	t.Run("failure zero matches every event", func(t *testing.T) {
		q := mustNewQueue(t, 10, 100, 100)
		if err := q.Add("p", 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.Pop(0); err != nil {
			t.Fatal(err)
		}
		if err := q.Event(128, 0); err != nil {
			t.Fatal(err)
		}
		if err := q.Done("p", OutcomeFailed, 0, 0); err != nil {
			t.Fatal(err)
		}
		if got := stateOf(q, "p"); got != stateBackoff {
			t.Fatalf("state = %d, want backoff", got)
		}
	})
}

func TestEventMovesOnlyMatchingParkedPods(t *testing.T) {
	q := mustNewQueue(t, 10, 100, 100)
	if err := q.Add("matching", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("not-matching", 0, 0); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"matching", "not-matching"} {
		if _, _, err := q.Pop(0); err != nil {
			t.Fatal(err)
		}
		failureBits := int64(4)
		if id == "not-matching" {
			failureBits = 8
		}
		if err := q.Done(id, OutcomeFailed, failureBits, 0); err != nil {
			t.Fatal(err)
		}
	}

	if err := q.Event(4, 5); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "matching"); got != stateBackoff {
		t.Fatalf("matching state = %d, want backoff", got)
	}
	if got := stateOf(q, "not-matching"); got != stateUnschedulable {
		t.Fatalf("not-matching state = %d, want unschedulable", got)
	}

	if err := q.Event(16, 6); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(q, "not-matching"); got != stateUnschedulable {
		t.Fatalf("not-matching state after second unrelated event = %d, want parked", got)
	}
}

func popID(t *testing.T, q *SchedulingQueue, now int64) string {
	t.Helper()
	pod, ok, err := q.Pop(now)
	if err != nil {
		t.Fatalf("Pop(%d): %v", now, err)
	}
	if !ok {
		t.Fatalf("Pop(%d): no pod", now)
	}
	return pod.ID
}

func TestPopOrderingAndRetriesKeepT0(t *testing.T) {
	q := mustNewQueue(t, 10, 100, 100)
	mustAdd := func(id string, priority int, now int64) {
		t.Helper()
		if err := q.Add(id, priority, now); err != nil {
			t.Fatal(err)
		}
	}

	mustAdd("high-old", 10, 1)
	mustAdd("high-old-byte", 10, 1)
	mustAdd("high-new", 10, 3)
	mustAdd("low", 1, 4)

	want := []string{"high-old", "high-old-byte", "high-new", "low"}
	for index, id := range want {
		if got := popID(t, q, int64(4+index)); got != id {
			t.Fatalf("pop %d = %s, want %s", index, got, id)
		}
	}

	if pod, ok, err := q.Pop(8); err != nil || ok || pod != nil {
		t.Fatalf("empty Pop = %#v,%v,%v; want nil,false,nil", pod, ok, err)
	}

	if err := q.Event(1, 10); err != nil {
		t.Fatal(err)
	}
	if err := q.Done("low", OutcomeFailed, 0, 10); err != nil {
		t.Fatal(err)
	}
	_ = q.Advance(20)

	mustAdd("new", 1, 20)
	if got := popID(t, q, 21); got != "low" {
		t.Fatalf("retry keeps original t0, got %s, want low", got)
	}
	if got := popID(t, q, 22); got != "new" {
		t.Fatalf("older retried pod should precede newer pod, got %s, want new", got)
	}
}

func TestRemoveInFlightThenDoneRejected(t *testing.T) {
	q := mustNewQueue(t, 10, 100, 100)
	if err := q.Add("p", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.Pop(0); err != nil {
		t.Fatal(err)
	}
	if err := q.Remove("p"); err != nil {
		t.Fatal(err)
	}
	if err := q.Done("p", OutcomeScheduled, 0, 1); err != ErrPodNotFound {
		t.Fatalf("Done after Remove = %v, want %v", err, ErrPodNotFound)
	}
	if sizes := q.Sizes(); sizes != (Sizes{}) {
		t.Fatalf("sizes after remove = %+v, want zero", sizes)
	}
}

func TestValidationOrderAndRejectionLeavesState(t *testing.T) {
	q := mustNewQueue(t, 10, 100, 100)
	if err := q.Add("p", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Event(1, 10); err != nil {
		t.Fatal(err)
	}

	if err := q.Add("", 0, -1); err != ErrInvalidArgument {
		t.Fatalf("empty id with negative now = %v, want invalid argument", err)
	}
	if err := q.Add("p", 0, 10); err != ErrPodAlreadyExists {
		t.Fatalf("duplicate add = %v, want exists", err)
	}
	if err := q.Add("p", 0, 9); err != ErrClockRollback {
		t.Fatalf("rollback add = %v, want rollback", err)
	}
	if err := q.Done("missing", Outcome(9), 256, -1); err != ErrInvalidArgument {
		t.Fatalf("invalid Done = %v, want invalid argument", err)
	}
	if err := q.Done("missing", OutcomeFailed, 0, 11); err != ErrPodNotFound {
		t.Fatalf("missing Done = %v, want not found", err)
	}
	if err := q.Remove("missing"); err != ErrPodNotFound {
		t.Fatalf("missing Remove = %v, want not found", err)
	}
	if err := q.Event(0, 8); err != ErrInvalidArgument {
		t.Fatalf("invalid Event = %v, want invalid argument", err)
	}

	entry := q.pods["p"]
	if entry.state != stateActive || entry.attempts != 0 || q.eventSeq != 1 || q.maxNow != 10 {
		t.Fatalf("state changed after rejection: state=%d attempts=%d events=%d maxNow=%d",
			entry.state, entry.attempts, q.eventSeq, q.maxNow)
	}
	if sizes := q.Sizes(); sizes != (Sizes{Active: 1}) {
		t.Fatalf("sizes after rejections = %+v, want one active", sizes)
	}
}
