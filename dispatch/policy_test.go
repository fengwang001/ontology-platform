package dispatch

import "testing"

// DropOldest: a full queue evicts the oldest message; the receiver observes
// the newest ones, and the dropped prefix is accounted for.
func TestDropOldest(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 2, OnFull: DropOldest})

	publishN(t, d, "e", "a", 4) // seqs 1..4; queue keeps 3,4

	assertSeqs(t, drainAfterCancel(s), []uint64{3, 4})
	assertCounters(t, s, 2, 2)
}

// DropNewest: a full queue rejects the incoming message; the receiver
// observes the oldest ones and the dropped suffix is accounted for.
func TestDropNewest(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 2, OnFull: DropNewest})

	publishN(t, d, "e", "a", 4) // seqs 1..4; queue keeps 1,2

	assertSeqs(t, drainAfterCancel(s), []uint64{1, 2})
	assertCounters(t, s, 2, 4)
}

// Disconnect: a full queue marks the subscriber lagging, drops the incoming
// message, discards pending ones, and closes the channel.
func TestDisconnect(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 1, OnFull: Disconnect})

	mustPublish(t, d, "e", "a") // seq 1 queued
	mustPublish(t, d, "e", "a") // seq 2 overflows and disconnects

	if !s.Lagging() {
		t.Fatal("expected Lagging() = true after overflow")
	}
	got := drain(s.C()) // pending seq 1 was discarded
	if len(got) != 0 {
		t.Fatalf("received %v, want nothing after disconnect", got)
	}
	assertCounters(t, s, 2, 1) // seq 2 dropped, then pending seq 1 discarded

	// A disconnected subscriber receives nothing further.
	mustPublish(t, d, "e", "a")
	assertCounters(t, s, 2, 1)
}

// One subscriber's drops never affect another subscriber in the same
// Publish: a slow DropNewest subscriber and a roomy subscriber both match,
// and the roomy one still receives everything.
func TestPoliciesAreIndependent(t *testing.T) {
	d := New()
	defer d.Close()
	slow := mustSubscribe(t, d, Options{Capacity: 1, OnFull: DropNewest})
	roomy := mustSubscribe(t, d, Options{Capacity: 16, OnFull: DropNewest})

	publishN(t, d, "e", "a", 8)

	assertSeqs(t, drainAfterCancel(roomy), []uint64{1, 2, 3, 4, 5, 6, 7, 8})
	assertCounters(t, roomy, 0, 0)

	assertSeqs(t, drainAfterCancel(slow), []uint64{1})
	assertCounters(t, slow, 7, 8)
}

// The gap between received seqs corresponds exactly to the dropped count,
// so a caller can reconstruct the missed interval.
func TestSeqGapMatchesDropped(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 2, OnFull: DropOldest})

	publishN(t, d, "e", "a", 4)
	got := drainAfterCancel(s)

	// With capacity 2 and DropOldest the queue holds the two newest:
	// after seqs 1..4 the queue is {3, 4}.
	assertSeqs(t, got, []uint64{3, 4})
	// The caller reconstructs the missed interval [1, 2] from the gap
	// before the first received seq; its size equals Dropped().
	missed := got[0] - 1
	if missed != s.Dropped() {
		t.Fatalf("missed %d != dropped %d", missed, s.Dropped())
	}
	if s.LastDroppedSeq() != 2 {
		t.Fatalf("LastDroppedSeq = %d, want 2", s.LastDroppedSeq())
	}
}

// drainAfterCancel cancels with the default Drain policy and reads out the
// remaining queued messages.
func drainAfterCancel(s *Subscription) []uint64 {
	s.Cancel()
	return drain(s.C())
}
