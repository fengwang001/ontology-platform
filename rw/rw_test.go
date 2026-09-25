package rw

import "testing"

// TestGrantCheckIsConstant proves the writer-preference decision reads
// the aggregate waiting-writer flag instead of scanning the queue:
// with m writers queued behind a held reader, one grant decision must
// inspect a constant number of waiter entries, independent of m.
func TestGrantCheckIsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := NewState()
		s.AddReader("R0") // a held reader forces every writer to wait
		for i := 0; i < m; i++ {
			s.WriterEnqueued()
		}
		if s.CanRead() {
			t.Fatalf("m=%d: reader allowed while %d writers wait", m, m)
		}
		if got := s.checked; got != 1 {
			t.Fatalf("m=%d: CanRead inspected %d waiter entries, want 1", m, got)
		}
		if s.CanWrite() {
			t.Fatalf("m=%d: writer allowed while reader held", m)
		}
		if got := s.checked; got != 1 {
			t.Fatalf("m=%d: CanWrite inspected %d waiter entries, want 1", m, got)
		}
		if !s.WriterWaiting() {
			t.Fatalf("m=%d: waiting-writer flag lost", m)
		}
	}
}
