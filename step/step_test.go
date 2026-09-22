package step

import (
	"errors"
	"testing"

	"ontology/journal"
)

func TestStateMachineLifecycleAndCounts(t *testing.T) {
	j := journal.New(0, nil)
	ex := NewExecutor(j)
	m := NewMachine("w")
	calls := 0
	compCalls := 0
	act := Action{
		Execute:    func() error { calls++; return nil },
		Compensate: func() error { compCalls++; return nil },
	}
	if err := ex.RunExecute(m, act); err != nil {
		t.Fatal(err)
	}
	if m.State() != Done || m.ExecCount() != 1 {
		t.Fatalf("after execute: %s %d", m.State(), m.ExecCount())
	}
	if err := ex.RunCompensate(m, act); err != nil {
		t.Fatal(err)
	}
	if m.State() != Compensated || m.CompCount() != 1 {
		t.Fatalf("after comp: %s %d", m.State(), m.CompCount())
	}
}

func TestFailedAttemptAllowsRetryWithoutDone(t *testing.T) {
	j := journal.New(0, nil)
	ex := NewExecutor(j)
	m := NewMachine("w")
	n := 0
	act := Action{Execute: func() error {
		n++
		if n < 3 {
			return errors.New("boom")
		}
		return nil
	}}
	// attempt 1 and 2 fail in-memory (start durable, no done); attempt 3 ok
	if err := ex.RunExecute(m, act); err == nil {
		t.Fatal("want error")
	}
	if err := ex.RunExecute(m, act); err == nil {
		t.Fatal("want error")
	}
	if err := ex.RunExecute(m, act); err != nil {
		t.Fatal(err)
	}
	if m.ExecCount() != 3 {
		t.Fatalf("exec count %d want 3", m.ExecCount())
	}
}

func TestReplayFoldEquivalence(t *testing.T) {
	j := journal.New(0, nil)
	recs := []struct {
		id    string
		phase journal.Phase
	}{
		{"a", journal.PhaseStart}, {"a", journal.PhaseDone},
		{"b", journal.PhaseStart}, {"b", journal.PhaseFailed},
	}
	for _, r := range recs {
		if _, err := j.Append(r.id, r.phase, ""); err != nil {
			t.Fatal(err)
		}
	}
	for k := 0; k < 3; k++ {
		rj, err := journal.Load(j.Bytes(), 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		ma, mb := NewMachine("a"), NewMachine("b")
		for _, r := range rj.Snapshot() {
			m := ma
			if r.StepID == "b" {
				m = mb
			}
			if err := m.Apply(r); err != nil {
				t.Fatal(err)
			}
		}
		if ma.State() != Done || mb.State() != Failed {
			t.Fatalf("fold %d: %s %s", k, ma.State(), mb.State())
		}
	}
}

func TestPanicBecomesError(t *testing.T) {
	j := journal.New(0, nil)
	ex := NewExecutor(j)
	m := NewMachine("w")
	act := Action{Execute: func() error { panic("x") }}
	if err := ex.RunExecute(m, act); err == nil {
		t.Fatal("panic should convert to error")
	}
	if m.State() != Running {
		t.Fatalf("no terminal record on panic, got %s", m.State())
	}
}
