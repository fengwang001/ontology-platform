package dispatch

import "testing"

// TestSeqGapsMatchDrops verifies the accounting invariant: for a subscriber
// that matched every published message, received + dropped equals
// published, and the gaps between received sequence numbers exactly account
// for the dropped count.
func TestSeqGapsMatchDrops(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "gap", EntityPrefix: "e", BufferSize: 2, Policy: DropNewest,
	})
	const published = 9
	for i := 0; i < published; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	ms := recvN(t, s, 2)
	if got := seqsOf(ms); !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("got seqs %v, want [1 2]", got)
	}
	if s.Dropped() != published-2 {
		t.Fatalf("Dropped() = %d, want %d", s.Dropped(), published-2)
	}
	// Gaps between received seqs must sum to the dropped count. Here the
	// gap is everything after seq 2: seqs 3..9 were dropped.
	var gaps uint64
	for i := 1; i < len(ms); i++ {
		gaps += ms[i].Seq - ms[i-1].Seq - 1
	}
	gaps += published - ms[len(ms)-1].Seq // dropped tail
	if gaps != s.Dropped() {
		t.Fatalf("seq gaps = %d, Dropped() = %d, must be equal", gaps, s.Dropped())
	}
	if uint64(len(ms))+s.Dropped() != published {
		t.Fatalf("received %d + dropped %d != published %d",
			len(ms), s.Dropped(), published)
	}
}

// TestSeqStrictlyIncreasing checks that every subscriber observes strictly
// increasing sequence numbers even while other subscribers drop messages.
func TestSeqStrictlyIncreasing(t *testing.T) {
	d := New()
	defer d.Close()
	slow := mustSubscribe(t, d, SubscribeOptions{
		ID: "slow", EntityPrefix: "e", BufferSize: 1, Policy: DropOldest,
	})
	fast := mustSubscribe(t, d, SubscribeOptions{
		ID: "fast", EntityPrefix: "e", BufferSize: 64, Policy: DropNewest,
	})
	const n = 50
	for i := 0; i < n; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	fastMsgs := recvN(t, fast, n)
	for i := 1; i < len(fastMsgs); i++ {
		if fastMsgs[i].Seq <= fastMsgs[i-1].Seq {
			t.Fatalf("fast: seqs not strictly increasing at %d: %v",
				i, seqsOf(fastMsgs))
		}
	}
	// The slow subscriber kept only the last message; its seq must be the
	// global last one and strictly greater than anything it saw before.
	slowMsgs := recvN(t, slow, 1)
	if slowMsgs[0].Seq != n {
		t.Fatalf("slow: last seq = %d, want %d", slowMsgs[0].Seq, n)
	}
	if slow.Dropped() != n-1 {
		t.Fatalf("slow: Dropped() = %d, want %d", slow.Dropped(), n-1)
	}
}

// TestLastDropSeqAttribution: the last dropped sequence number identifies
// exactly which message was lost most recently, per policy.
func TestLastDropSeqAttribution(t *testing.T) {
	d := New()
	defer d.Close()
	oldest := mustSubscribe(t, d, SubscribeOptions{
		ID: "o", EntityPrefix: "e", BufferSize: 1, Policy: DropOldest,
	})
	newest := mustSubscribe(t, d, SubscribeOptions{
		ID: "n", EntityPrefix: "x", BufferSize: 1, Policy: DropNewest,
	})
	mustPublish(t, d, "e1", "p", nil) // seq 1
	mustPublish(t, d, "e1", "p", nil) // seq 2: evicts seq 1 for "o"
	mustPublish(t, d, "x1", "p", nil) // seq 3
	mustPublish(t, d, "x1", "p", nil) // seq 4: rejected for "n"
	if got := oldest.LastDropSeq(); got != 1 {
		t.Fatalf("oldest LastDropSeq = %d, want 1", got)
	}
	if got := newest.LastDropSeq(); got != 4 {
		t.Fatalf("newest LastDropSeq = %d, want 4", got)
	}
}
