package timer

import (
	"testing"

	"ontology/slot"
)

func TestStateMachine(t *testing.T) {
	tests := []struct {
		name  string
		ops   []func(*Timer) bool
		wants []bool
		final State
	}{
		{"fire once", []func(*Timer) bool{
			func(tm *Timer) bool { return tm.Fire() },
			func(tm *Timer) bool { return tm.Fire() },
		}, []bool{true, false}, Fired},
		{"cancel once", []func(*Timer) bool{
			func(tm *Timer) bool { return tm.Cancel() },
			func(tm *Timer) bool { return tm.Cancel() },
		}, []bool{true, false}, Cancelled},
		{"cancel after fire fails", []func(*Timer) bool{
			func(tm *Timer) bool { return tm.Fire() },
			func(tm *Timer) bool { return tm.Cancel() },
		}, []bool{true, false}, Fired},
		{"fire after cancel fails", []func(*Timer) bool{
			func(tm *Timer) bool { return tm.Cancel() },
			func(tm *Timer) bool { return tm.Fire() },
		}, []bool{true, false}, Cancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := New(1, 1, 10, nil)
			for i, op := range tt.ops {
				if got := op(tm); got != tt.wants[i] {
					t.Fatalf("op %d = %v, want %v", i, got, tt.wants[i])
				}
			}
			if tm.State() != tt.final {
				t.Fatalf("final state = %d, want %d", tm.State(), tt.final)
			}
		})
	}
}

func TestAttachDetach(t *testing.T) {
	var s slot.Slot
	tm := New(7, 1, 10, nil)
	tm.Attach(&s, 2, 3)
	s.Insert(tm.ID, tm)
	if l, i := tm.Location(); l != 2 || i != 3 {
		t.Fatalf("location = (%d,%d)", l, i)
	}
	tm.Detach()
	if s.Len() != 0 {
		t.Fatalf("slot still holds timer after Detach")
	}
	if l, i := tm.Location(); l != -1 || i != -1 {
		t.Fatalf("location after detach = (%d,%d)", l, i)
	}
	tm.Detach()
}

func TestGen(t *testing.T) {
	tm := New(1, 1, 0, nil)
	if tm.Gen() != 0 {
		t.Fatalf("initial gen = %d", tm.Gen())
	}
	tm.BumpGen()
	if tm.Gen() != 1 {
		t.Fatalf("gen after bump = %d", tm.Gen())
	}
}
