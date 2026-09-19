package dispatch

import "testing"

// TestDropOldestPolicy: a full queue evicts the oldest message; the
// subscriber receives the newest ones and the evictions are metered.
func TestDropOldestPolicy(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "oldest", EntityPrefix: "e", BufferSize: 2, Policy: DropOldest,
	})
	for i := 0; i < 4; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	got := seqsOf(recvN(t, s, 2))
	if !equalSeqs(got, []uint64{3, 4}) {
		t.Fatalf("got seqs %v, want [3 4]", got)
	}
	if s.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", s.Dropped())
	}
	if s.LastDropSeq() != 2 {
		t.Fatalf("LastDropSeq() = %d, want 2 (evicted message)", s.LastDropSeq())
	}
}

// TestDropNewestPolicy: a full queue rejects the incoming message; the
// subscriber keeps the oldest ones and rejections are metered.
func TestDropNewestPolicy(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "newest", EntityPrefix: "e", BufferSize: 2, Policy: DropNewest,
	})
	for i := 0; i < 4; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	got := seqsOf(recvN(t, s, 2))
	if !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("got seqs %v, want [1 2]", got)
	}
	if s.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", s.Dropped())
	}
	if s.LastDropSeq() != 4 {
		t.Fatalf("LastDropSeq() = %d, want 4 (rejected message)", s.LastDropSeq())
	}
}

// TestDisconnectPolicy: a full queue marks the subscriber lagging and
// disconnects it; later publishes are not delivered to it.
func TestDisconnectPolicy(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "disc", EntityPrefix: "e", BufferSize: 1, Policy: Disconnect,
	})
	mustPublish(t, d, "e1", "p", 1)
	mustPublish(t, d, "e1", "p", 2) // fills queue -> disconnect
	if !s.Lagged() {
		t.Fatal("Lagged() = false, want true")
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done() not closed after lagging disconnect")
	}
	mustPublish(t, d, "e1", "p", 3) // must not reach s
	got := drainClosed(s)
	if !equalSeqs(seqsOf(got), []uint64{1}) {
		t.Fatalf("drained seqs %v, want [1]", seqsOf(got))
	}
	if s.Dropped() != 1 || s.LastDropSeq() != 2 {
		t.Fatalf("Dropped=%d LastDropSeq=%d, want 1 and 2", s.Dropped(), s.LastDropSeq())
	}
	if ids := d.Match("e1", "p"); len(ids) != 0 {
		t.Fatalf("disconnected subscriber still matched: %v", ids)
	}
}

// TestPoliciesIndependent: in one Publish fan-out, one subscriber's drops
// must not affect another subscriber receiving everything.
func TestPoliciesIndependent(t *testing.T) {
	d := New()
	defer d.Close()
	oldest := mustSubscribe(t, d, SubscribeOptions{
		ID: "a", EntityPrefix: "e", BufferSize: 1, Policy: DropOldest,
	})
	newest := mustSubscribe(t, d, SubscribeOptions{
		ID: "b", EntityPrefix: "e", BufferSize: 1, Policy: DropNewest,
	})
	full := mustSubscribe(t, d, SubscribeOptions{
		ID: "c", EntityPrefix: "e", BufferSize: 8, Policy: DropNewest,
	})
	for i := 0; i < 4; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	if got := seqsOf(recvN(t, oldest, 1)); !equalSeqs(got, []uint64{4}) {
		t.Fatalf("oldest got %v, want [4]", got)
	}
	if got := seqsOf(recvN(t, newest, 1)); !equalSeqs(got, []uint64{1}) {
		t.Fatalf("newest got %v, want [1]", got)
	}
	if got := seqsOf(recvN(t, full, 4)); !equalSeqs(got, []uint64{1, 2, 3, 4}) {
		t.Fatalf("full got %v, want [1 2 3 4]", got)
	}
	if oldest.Dropped() != 3 || newest.Dropped() != 3 || full.Dropped() != 0 {
		t.Fatalf("drops: oldest=%d newest=%d full=%d, want 3/3/0",
			oldest.Dropped(), newest.Dropped(), full.Dropped())
	}
}
