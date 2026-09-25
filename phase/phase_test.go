package phase

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

type shot struct {
	phase     int
	parties   map[int]struct{}
	unarrived map[int]struct{}
}

func snap(s *State) shot {
	ph, pp, uu := s.Snapshot()
	return shot{ph, pp, uu}
}

func setEq(a, b map[int]struct{}) bool { return reflect.DeepEqual(a, b) }

// TestAdvancePrecision pins invariant 2: advance happens iff unarrived hits 0
// with parties > 0 — never early, never late.
func TestAdvancePrecision(t *testing.T) {
	s, _ := New(2) // A=0 B=1
	if _, err := s.Arrive(0); err != nil {
		t.Fatal(err)
	}
	id, ph, err := s.Register() // C joins CURRENT phase 0
	if err != nil || id != 2 || ph != 0 {
		t.Fatalf("register: id=%d ph=%d err=%v", id, ph, err)
	}
	if _, err := s.Arrive(1); err != nil || s.Phase() != 0 {
		t.Fatalf("after B arrives phase must stay 0, got %d err=%v", s.Phase(), err)
	}
	p, err := s.Arrive(2) // C is last: advances 0->1, returns pre-advance 0
	if err != nil || p != 0 || s.Phase() != 1 {
		t.Fatalf("C arrive: returned=%d phase=%d err=%v", p, s.Phase(), err)
	}
	// Scenario (乙): B arrives, then A arrive-and-deregister => phase exactly 1.
	s2, _ := New(2)
	if _, err := s2.Arrive(1); err != nil {
		t.Fatal(err)
	}
	if p, err := s2.ArriveAndDeregister(0); err != nil || p != 0 || s2.Phase() != 1 {
		t.Fatalf("arrive+deregister: ret=%d phase=%d err=%v", p, s2.Phase(), err)
	}
}

// TestPhaseMonotonic pins invariant 3: over 5 shuffled rounds every advance is
// exactly +1.
func TestPhaseMonotonic(t *testing.T) {
	s, _ := New(4)
	r := rand.New(rand.NewSource(1))
	for round := 1; round <= 5; round++ {
		ids := []int{0, 1, 2, 3}
		r.Shuffle(4, func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		for i, id := range ids {
			p, err := s.Arrive(id)
			if err != nil {
				t.Fatal(err)
			}
			if p != round-1 {
				t.Fatalf("arrival returned phase %d, want %d", p, round-1)
			}
			if i < 3 && s.Phase() != round-1 {
				t.Fatal("advanced before last arrival")
			}
		}
		if s.Phase() != round {
			t.Fatalf("phase=%d, want %d", s.Phase(), round)
		}
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4 with a table of all four
// distinct error kinds; state before and after must be deeply equal.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*State)
		op    func(*State) error
		want  error
	}{
		{"bad n", nil, func(s *State) error { _, e := New(0); return e }, ErrBadN},
		{"unknown id", func(s *State) {}, func(s *State) error { _, e := s.Arrive(7); return e }, ErrUnknownParty},
		{"duplicate arrival", func(s *State) { s.Arrive(0) }, func(s *State) error { _, e := s.Arrive(0); return e }, ErrDuplicateArrival},
		{"after terminated", func(s *State) { s.ArriveAndDeregister(0); s.ArriveAndDeregister(1) },
			func(s *State) error { _, e := s.Arrive(0); return e }, ErrTerminated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := New(2)
			if c.setup != nil {
				c.setup(s)
			}
			before := snap(s)
			err := c.op(s)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			after := snap(s)
			if after.phase != before.phase || !setEq(after.parties, before.parties) || !setEq(after.unarrived, before.unarrived) {
				t.Fatalf("state changed by rejected op: %d %v %v vs %d %v %v",
					after.phase, after.parties, after.unarrived, before.phase, before.parties, before.unarrived)
			}
		})
	}
}

// TestArriveProbeO1 proves the advance decision examines exactly 1 party
// regardless of m: the last Arrive that triggers the advance leaves the
// unexported probe at 1.
func TestArriveProbeO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s, _ := New(m)
		order := rand.New(rand.NewSource(int64(m))).Perm(m)
		for _, id := range order[:m-1] {
			if _, err := s.Arrive(id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Arrive(order[m-1]); err != nil {
			t.Fatal(err)
		}
		if s.arriveProbe != 1 {
			t.Fatalf("m=%d probe=%d, want 1", m, s.arriveProbe)
		}
	}
}
