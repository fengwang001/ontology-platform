package dispatch

import "testing"

// mustSubscribe subscribes or fails the test.
func mustSubscribe(t *testing.T, d *Dispatcher, opts Options) *Subscription {
	t.Helper()
	s, err := d.Subscribe(opts)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return s
}

// mustPublish publishes or fails the test, returning the assigned seq.
func mustPublish(t *testing.T, d *Dispatcher, entity, attr string) uint64 {
	t.Helper()
	seq, err := d.Publish(entity, attr, nil)
	if err != nil {
		t.Fatalf("Publish(%s, %s): %v", entity, attr, err)
	}
	return seq
}

// publishN publishes n messages for (entity, attr) and returns their seqs.
func publishN(t *testing.T, d *Dispatcher, entity, attr string, n int) []uint64 {
	t.Helper()
	seqs := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		seqs = append(seqs, mustPublish(t, d, entity, attr))
	}
	return seqs
}

// drain reads until the channel is closed and returns the received seqs.
func drain(ch <-chan Message) []uint64 {
	var seqs []uint64
	for m := range ch {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

// recvOne reads exactly one message or fails.
func recvOne(t *testing.T, s *Subscription) Message {
	t.Helper()
	m, ok := <-s.C()
	if !ok {
		t.Fatalf("sub %d: channel closed, expected a message", s.ID())
	}
	return m
}

// assertSeqs fails unless got equals want.
func assertSeqs(t *testing.T, got, want []uint64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("seqs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seqs = %v, want %v", got, want)
		}
	}
}

// assertCounters checks dropped count and last dropped seq.
func assertCounters(t *testing.T, s *Subscription, dropped, lastSeq uint64) {
	t.Helper()
	if got := s.Dropped(); got != dropped {
		t.Fatalf("sub %d: Dropped() = %d, want %d", s.ID(), got, dropped)
	}
	if got := s.LastDroppedSeq(); got != lastSeq {
		t.Fatalf("sub %d: LastDroppedSeq() = %d, want %d", s.ID(), got, lastSeq)
	}
}
