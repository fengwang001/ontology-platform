package step

import (
	"testing"

	"ontology/journal"
)

func TestFoldLifecycle(t *testing.T) {
	var s Status
	s = Fold(s, journal.Record{Phase: journal.ExecStart, Attempt: 1})
	if s.State != Running || s.ExecCount() != 1 {
		t.Fatalf("after start: %+v", s)
	}
	s = Fold(s, journal.Record{Phase: journal.ExecDone, OK: false, Err: "boom"})
	if s.State != Failed {
		t.Fatalf("after failed done: %+v", s)
	}
	s = Fold(s, journal.Record{Phase: journal.ExecStart, Attempt: 2})
	s = Fold(s, journal.Record{Phase: journal.ExecDone, OK: true})
	if s.State != Completed || s.ExecCount() != 2 {
		t.Fatalf("after retry success: %+v", s)
	}
	s = Fold(s, journal.Record{Phase: journal.CompStart})
	s = Fold(s, journal.Record{Phase: journal.CompDone, OK: true})
	if s.State != Compensated || s.CompCount() != 1 {
		t.Fatalf("after comp: %+v", s)
	}
}

func TestFoldDeterministicReplay(t *testing.T) {
	recs := []journal.Record{
		{StepID: "a", Phase: journal.ExecStart},
		{StepID: "a", Phase: journal.ExecDone, OK: true},
		{StepID: "a", Phase: journal.CompStart},
		{StepID: "a", Phase: journal.CompDone, OK: false, Err: "undo boom"},
	}
	fold := func() Status {
		s := Status{State: Pending}
		for _, r := range recs {
			s = Fold(s, r)
		}
		return s
	}
	a, b, c := fold(), fold(), fold()
	if a != b || b != c {
		t.Fatalf("folds differ: %+v %+v %+v", a, b, c)
	}
	if a.State != CompFailed {
		t.Fatalf("want comp_failed, got %s", a.State)
	}
}

func TestTableMarkersAndUnknown(t *testing.T) {
	tab := NewTable([]string{"a"})
	tab.Apply(journal.Record{Phase: journal.Compensating})
	tab.Apply(journal.Record{Phase: journal.AllCompensated})
	if !tab.Compensating || tab.Terminal != journal.AllCompensated {
		t.Fatalf("table = %+v", tab)
	}
	if got := tab.Get("missing"); got.State != "" {
		t.Fatalf("unknown step must be zero value, got %+v", got)
	}
}
