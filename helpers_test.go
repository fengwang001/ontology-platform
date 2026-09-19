package ontology

import "testing"

func mustSubscribe(t *testing.T, d *Dispatcher, opts SubscribeOptions) *Subscription {
	t.Helper()
	s, err := d.Subscribe(opts)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return s
}

func mustPublish(t *testing.T, d *Dispatcher, entity, property string, value any) {
	t.Helper()
	if err := d.Publish(entity, property, value); err != nil {
		t.Fatalf("Publish(%q, %q): %v", entity, property, err)
	}
}

// tryRecvSeqs drains everything currently queued without blocking.
func tryRecvSeqs(s *Subscription) []uint64 {
	var seqs []uint64
	for {
		m, ok := s.TryReceive()
		if !ok {
			return seqs
		}
		seqs = append(seqs, m.Seq)
	}
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// countGaps returns how many sequence numbers in [1, max] are missing
// from the (strictly increasing) received list.
func countGaps(received []uint64, max uint64) uint64 {
	seen := make(map[uint64]bool, len(received))
	for _, s := range received {
		seen[s] = true
	}
	var missing uint64
	for i := uint64(1); i <= max; i++ {
		if !seen[i] {
			missing++
		}
	}
	return missing
}
