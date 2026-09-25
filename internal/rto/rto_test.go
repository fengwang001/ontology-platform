package rto

import "testing"

// TestProbeCountBounded is a white-box test: the unexported counter
// lastTickProbed must stay at a small constant independent of the number
// m of pending segments, proving the timeout check looks only at the
// earliest segment instead of scanning the whole pending set.
func TestProbeCountBounded(t *testing.T) {
	cases := []struct {
		name string
		m    int
	}{
		{"m=100", 100},
		{"m=1000", 1000},
		{"m=10000", 10000},
	}
	first := -1
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(10)
			for i := 0; i < c.m; i++ {
				s.Send(int64(i), 0)
			}
			// now well before the deadline: no timeout, no scan growth.
			if to, _ := s.Probe(5); to {
				t.Fatalf("unexpected timeout at now=5")
			}
			if s.lastTickProbed > 1 {
				t.Fatalf("inspected %d segments with m=%d, want <= 1", s.lastTickProbed, c.m)
			}
			if first == -1 {
				first = s.lastTickProbed
			} else if s.lastTickProbed != first {
				t.Fatalf("inspected count %d varies with m, want constant %d", s.lastTickProbed, first)
			}
			// A firing check also inspects exactly one (the earliest).
			to, seq := s.Probe(10)
			if !to || seq != 0 || s.lastTickProbed != 1 {
				t.Fatalf("firing probe: to=%v seq=%d inspected=%d", to, seq, s.lastTickProbed)
			}
		})
	}
}

// TestBackoffAndReset checks the doubling/reset rules at the rto layer.
func TestBackoffAndReset(t *testing.T) {
	s := New(10)
	s.Send(0, 0)
	steps := []struct {
		now      int64
		rto      int64
		deadline int64
		backoff  int
		timeout  bool
	}{
		{10, 20, 30, 1, true},
		{20, 20, 30, 1, false},
		{30, 40, 70, 2, true},
	}
	for i, w := range steps {
		to, _ := s.Probe(w.now)
		if to != w.timeout || s.RTO() != w.rto || s.Deadline() != w.deadline || s.Backoff() != w.backoff {
			t.Fatalf("step %d: to=%v rto=%d dl=%d bo=%d", i, to, s.RTO(), s.Deadline(), s.Backoff())
		}
	}
	// An advancing ACK that drains the set resets rto and stops the timer.
	if !s.ApplyAck(1, 40) {
		t.Fatal("Ack should advance base")
	}
	if s.RTO() != 10 || s.Backoff() != 0 || s.HasDeadline() {
		t.Fatalf("reset failed: rto=%d bo=%d armed=%v", s.RTO(), s.Backoff(), s.HasDeadline())
	}
	// The next Send starts from baseRTO again, not the backed-off value.
	s.Send(1, 50)
	if s.RTO() != 10 || s.Deadline() != 60 {
		t.Fatalf("send after reset: rto=%d dl=%d", s.RTO(), s.Deadline())
	}
}
