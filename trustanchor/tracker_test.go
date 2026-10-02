package trustanchor

import (
	"fmt"
	"testing"
)

func entry(key string, revoked ...bool) Entry {
	return Entry{Key: []byte(key), Revoked: len(revoked) > 0 && revoked[0]}
}

func keys(names ...string) [][]byte {
	out := make([][]byte, len(names))
	for i, name := range names {
		out[i] = []byte(name)
	}
	return out
}

func signers(names ...string) [][]byte { return keys(names...) }

func rejectReason(err error) string {
	if err == nil {
		return ""
	}
	if reject, ok := err.(*RejectError); ok {
		return reject.Reason
	}
	return err.Error()
}

func requireState(t *testing.T, tracker *Tracker, key, status string, since, at int64, count int) {
	t.Helper()
	got := tracker.State([]byte(key))
	if got.Status != status || got.Since != since || got.At != at || got.Count != count {
		t.Fatalf("%s = %+v; want status=%s since=%d at=%d count=%d", key, got, status, since, at, count)
	}
}

func newTestTracker(t *testing.T, h, r int64, kmax, m, q int, anchors ...string) *Tracker {
	t.Helper()
	tracker, err := New(h, r, kmax, m, q, keys(anchors...)...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tracker
}

func TestAddPendHoldAndContinuousAppearances(t *testing.T) {
	tracker := newTestTracker(t, 30, 100, 10, 1, 1, "k1", "k2")

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 10); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusAddPend, 10, 0, 1)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k2"), 39); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusAddPend, 10, 0, 2)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 40); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusValid, 0, 0, 0)
}

func TestAddPendAbsenceAndRevocationReset(t *testing.T) {
	tracker := newTestTracker(t, 30, 100, 10, 1, 1, "k1", "k2")

	for _, now := range []int64{10, 20} {
		if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), now); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, tracker, "k3", StatusAddPend, 10, 0, 2)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2")}, signers("k1"), 25); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusUntracked, 0, 0, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 35); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusAddPend, 35, 0, 1)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3", true)}, signers("k1"), 36); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusUntracked, 0, 0, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("unknown", true)}, signers("k1"), 37); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "unknown", StatusUntracked, 0, 0, 0)
}

func TestRequiredAppearances(t *testing.T) {
	tracker := newTestTracker(t, 30, 100, 10, 3, 1, "k1")

	for _, now := range []int64{10, 20} {
		if err := tracker.Observe([]Entry{entry("k1"), entry("k3")}, signers("k1"), now); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, tracker, "k3", StatusAddPend, 10, 0, 2)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k3")}, signers("k1"), 45); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusValid, 0, 0, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k4")}, signers("k1"), 50); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Observe([]Entry{entry("k1"), entry("k4")}, signers("k1"), 80); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k4", StatusAddPend, 50, 0, 2)
	if err := tracker.Observe([]Entry{entry("k1"), entry("k4")}, signers("k1"), 81); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k4", StatusValid, 0, 0, 0)
}

func TestRevocationMissingRetentionAndBanned(t *testing.T) {
	tracker := newTestTracker(t, 0, 100, 10, 1, 1, "k1", "k2", "k3")

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2", true), entry("k3")}, signers("k2"), 50); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusRevoked, 0, 50, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2", true), entry("k3")}, signers("k1"), 60); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusRevoked, 0, 50, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 70); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusRevoked, 0, 50, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k3")}, signers("k1"), 149); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusRevoked, 0, 50, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k3")}, signers("k1"), 150); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusBanned, 0, 50, 0)

	before := tracker.processed
	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 160); err != nil {
		t.Fatal(err)
	}
	if tracker.processed-before != int64(2+2) {
		t.Fatalf("processed delta = %d; banned key must not be counted as tracked", tracker.processed-before)
	}
	requireState(t, tracker, "k2", StatusBanned, 0, 50, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2", true), entry("k3")}, signers("k1"), 170); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusBanned, 0, 50, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2")}, signers("k1"), 180); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusMissing, 180, 0, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 181); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusValid, 0, 0, 0)

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2")}, signers("k1"), 200); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Observe([]Entry{entry("k1"), entry("k2")}, signers("k1"), 300); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k3", StatusUntracked, 0, 0, 0)
}

func TestQuorumAndPreObservationSignerState(t *testing.T) {
	if _, err := New(0, 100, 10, 1, 2, keys("k1")...); err == nil || rejectReason(err) != ReasonInvalidConfig {
		t.Fatalf("one anchor with Q=2 error = %v", err)
	}

	tracker := newTestTracker(t, 0, 100, 10, 1, 2, "k1", "k2", "k3")
	cases := [][][]byte{
		signers("k1"),
		signers("k1", "k1"),
		signers("k1", "x"),
		signers("k1", "k4"),
	}
	for i, names := range cases {
		err := tracker.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, names, 10)
		if err == nil || rejectReason(err) != ReasonUntrusted {
			t.Fatalf("case %d error = %v", i, err)
		}
	}

	if err := tracker.Observe([]Entry{entry("k1"), entry("k2", true), entry("k3")}, signers("k1", "k2", "k1", "x"), 20); err != nil {
		t.Fatal(err)
	}
	requireState(t, tracker, "k2", StatusRevoked, 0, 20, 0)

	err := tracker.Observe([]Entry{entry("k1"), entry("k3", true)}, signers("k3"), 30)
	if err == nil || rejectReason(err) != ReasonUntrusted {
		t.Fatalf("single VALID signer after revocation error = %v", err)
	}

	err = tracker.Observe([]Entry{entry("k1"), entry("k3", true)}, signers("k1", "k3"), 30)
	if err == nil || rejectReason(err) != ReasonDeadlock {
		t.Fatalf("post-processing quorum error = %v", err)
	}
	requireState(t, tracker, "k3", StatusValid, 0, 0, 0)
	requireState(t, tracker, "k2", StatusRevoked, 0, 20, 0)
}

func TestPromotionCanPreventDeadlock(t *testing.T) {
	accepted := newTestTracker(t, 0, 100, 10, 1, 2, "k1", "k2")
	if err := accepted.Observe([]Entry{entry("k1", true), entry("k2"), entry("k3")}, signers("k1", "k2"), 10); err != nil {
		t.Fatal(err)
	}
	requireState(t, accepted, "k1", StatusRevoked, 0, 10, 0)
	requireState(t, accepted, "k3", StatusValid, 0, 0, 0)

	blocked := newTestTracker(t, 1, 100, 10, 1, 2, "k1", "k2")
	err := blocked.Observe([]Entry{entry("k1", true), entry("k2"), entry("k3")}, signers("k1", "k2"), 10)
	if err == nil || rejectReason(err) != ReasonDeadlock {
		t.Fatalf("H>0 error = %v", err)
	}
	requireState(t, blocked, "k1", StatusValid, 0, 0, 0)
	requireState(t, blocked, "k3", StatusUntracked, 0, 0, 0)
}

func TestDeadlockPrecedesLimitAndLimitUsesFinalTrackedCount(t *testing.T) {
	tracker := newTestTracker(t, 30, 100, 3, 1, 2, "k1", "k2")
	err := tracker.Observe([]Entry{entry("k1"), entry("k2", true), entry("k3"), entry("k4")}, signers("k1", "k2"), 10)
	if err == nil || rejectReason(err) != ReasonDeadlock {
		t.Fatalf("deadlock+limit error = %v", err)
	}
	requireState(t, tracker, "k2", StatusValid, 0, 0, 0)
	requireState(t, tracker, "k3", StatusUntracked, 0, 0, 0)

	limited := newTestTracker(t, 0, 100, 3, 1, 1, "k1")
	if err := limited.Observe([]Entry{entry("k1"), entry("k2"), entry("k3")}, signers("k1"), 10); err != nil {
		t.Fatal(err)
	}
	err = limited.Observe([]Entry{entry("k1"), entry("k2"), entry("k3"), entry("k4")}, signers("k1"), 20)
	if err == nil || rejectReason(err) != ReasonLimit {
		t.Fatalf("limit error = %v", err)
	}
	requireState(t, limited, "k4", StatusUntracked, 0, 0, 0)
}

func TestInvalidArgumentsAndClockRollbackPriority(t *testing.T) {
	tracker := newTestTracker(t, 0, 100, 10, 1, 1, "k1")

	cases := []struct {
		name    string
		entries []Entry
		signers [][]byte
		now     int64
	}{
		{"empty entries", nil, signers("k1"), 1},
		{"65 entries", duplicateEntries(65), signers("k1"), 1},
		{"duplicate keys", []Entry{entry("k1"), entry("k1")}, signers("k1"), 1},
		{"empty key", []Entry{{Key: nil}}, signers("k1"), 1},
		{"empty signer", []Entry{entry("k1")}, [][]byte{nil}, 1},
		{"negative now", []Entry{entry("k1")}, signers("k1"), -1},
	}
	for _, tc := range cases {
		if err := tracker.Observe(tc.entries, tc.signers, tc.now); err == nil || rejectReason(err) != ReasonInvalidArgs {
			t.Fatalf("%s error = %v", tc.name, err)
		}
	}

	if err := tracker.Observe([]Entry{entry("k1")}, signers("k1"), 20); err != nil {
		t.Fatal(err)
	}
	before := tracker.State([]byte("k1"))
	beforeProcessed := tracker.processed
	err := tracker.Observe([]Entry{entry("k1"), entry("k2")}, nil, 19)
	if err == nil || rejectReason(err) != ReasonClockRollback {
		t.Fatalf("rollback with no valid signers error = %v", err)
	}
	if got := tracker.State([]byte("k1")); got != before || tracker.processed != beforeProcessed {
		t.Fatalf("rejected observation changed state: %+v -> %+v", before, got)
	}
}

func duplicateEntries(n int) []Entry {
	out := make([]Entry, n)
	for i := range out {
		out[i] = entry("k")
	}
	return out
}

func TestProcessedCountBound(t *testing.T) {
	tracker := newTestTracker(t, 0, 100, 10, 1, 1, "k1")
	observations := [][]Entry{
		{entry("k1"), entry("k2"), entry("k3")},
		{entry("k1"), entry("k2"), entry("k3"), entry("k4")},
		{entry("k1"), entry("k3"), entry("k4")},
		{entry("k1"), entry("k4")},
		{entry("k1"), entry("k2", true)},
	}
	for i, entries := range observations {
		trackedBefore := len(tracker.keys)
		processedBefore := tracker.processed
		if err := tracker.Observe(entries, signers("k1"), int64(10*(i+1))); err != nil {
			t.Fatal(err)
		}
		delta := tracker.processed - processedBefore
		bound := int64(trackedBefore + len(entries))
		if delta > bound {
			t.Fatalf("processed delta %d exceeds entries(%d)+tracked-before(%d)", delta, len(entries), trackedBefore)
		}
	}
}

func TestConcurrentObservationsAndStates(t *testing.T) {
	tracker := newTestTracker(t, 0, 1_000_000_000, 100, 1, 2, "a0", "a1", "a2", "a3", "a4")
	start := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		<-start
		for i := 0; i < 2000; i++ {
			state := tracker.State([]byte("a0"))
			if state.Status != StatusValid {
				t.Errorf("concurrent read saw a0=%s", state.Status)
				return
			}
		}
	}()

	const writers = 40
	finished := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		go func(id int) {
			<-start
			for step := 0; step < 2; step++ {
				now := int64(1 + id*10 + step)
				entries := []Entry{
					entry("a0"), entry("a1"), entry("a2"), entry("a3"), entry("a4"),
					entry(fmt.Sprintf("w%d-%d", id, step)),
				}
				err := tracker.Observe(entries, signers("a0", "a1"), now)
				if err != nil && rejectReason(err) != ReasonClockRollback && rejectReason(err) != ReasonLimit {
					finished <- err
					return
				}
			}
			finished <- nil
		}(writer)
	}
	close(start)
	for writer := 0; writer < writers; writer++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	<-done

	for _, anchor := range []string{"a0", "a1", "a2", "a3", "a4"} {
		requireState(t, tracker, anchor, StatusValid, 0, 0, 0)
	}
}
