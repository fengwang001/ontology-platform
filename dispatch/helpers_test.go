package dispatch

import "testing"

// mustSubscribe subscribes or fails the test.
func mustSubscribe(t *testing.T, d *Dispatcher, opts SubscribeOptions) *Subscriber {
	t.Helper()
	s, err := d.Subscribe(opts)
	if err != nil {
		t.Fatalf("Subscribe(%q): %v", opts.ID, err)
	}
	return s
}

// mustPublish publishes or fails the test.
func mustPublish(t *testing.T, d *Dispatcher, entity, property string, value any) {
	t.Helper()
	if err := d.Publish(entity, property, value); err != nil {
		t.Fatalf("Publish(%q, %q): %v", entity, property, err)
	}
}

// recvN reads exactly n messages. Because Publish is synchronous, messages
// are already queued when Publish returns, so this never needs to sleep.
func recvN(t *testing.T, s *Subscriber, n int) []Message {
	t.Helper()
	ms := make([]Message, 0, n)
	for i := 0; i < n; i++ {
		m, ok := <-s.Chan()
		if !ok {
			t.Fatalf("subscriber %q: channel closed after %d/%d messages", s.ID(), i, n)
		}
		ms = append(ms, m)
	}
	return ms
}

// tryRecv performs a non-blocking receive.
func tryRecv(s *Subscriber) (Message, bool) {
	select {
	case m, ok := <-s.Chan():
		return m, ok
	default:
		return Message{}, false
	}
}

// seqsOf extracts the sequence numbers of ms.
func seqsOf(ms []Message) []uint64 {
	seqs := make([]uint64, len(ms))
	for i, m := range ms {
		seqs[i] = m.Seq
	}
	return seqs
}

// equalSeqs reports whether got equals want element-wise.
func equalSeqs(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// drainClosed reads until the channel is closed and returns all messages.
func drainClosed(s *Subscriber) []Message {
	var ms []Message
	for m := range s.Chan() {
		ms = append(ms, m)
	}
	return ms
}
