package step

import (
	"testing"

	"ontology/journal"
)

func applyAll(m *Machine, phases ...journal.Phase) {
	for i, p := range phases {
		m.Apply(journal.Record{Seq: uint64(i + 1), Phase: p, StepID: "s"})
	}
}

func TestMachineLifecycle(t *testing.T) {
	m := NewMachine()
	if m.Status() != Pending {
		t.Fatalf("initial = %s", m.Status())
	}
	applyAll(m, journal.PhaseStart, journal.PhaseRetry, journal.PhaseSuccess)
	if m.Status() != Succeeded {
		t.Fatalf("status = %s", m.Status())
	}
	exec, comp := m.Counts()
	if exec != 2 || comp != 0 {
		t.Fatalf("counts = %d/%d", exec, comp)
	}
	applyAll(m, journal.PhaseCompStart, journal.PhaseCompOK)
	if m.Status() != Compensated {
		t.Fatalf("status = %s", m.Status())
	}
	exec, comp = m.Counts()
	if exec != 2 || comp != 1 {
		t.Fatalf("counts = %d/%d", exec, comp)
	}
}

func TestMachineFailureStates(t *testing.T) {
	m := NewMachine()
	m.Apply(journal.Record{Seq: 1, Phase: journal.PhaseStart, StepID: "s"})
	m.Apply(journal.Record{Seq: 2, Phase: journal.PhaseFailure, StepID: "s", Note: "boom"})
	if m.Status() != Failed || m.Note() != "boom" {
		t.Fatalf("status=%s note=%q", m.Status(), m.Note())
	}
	m2 := NewMachine()
	applyAll(m2, journal.PhaseStart, journal.PhaseSuccess,
		journal.PhaseCompStart, journal.PhaseCompFail)
	if m2.Status() != CompensateFailed {
		t.Fatalf("status = %s", m2.Status())
	}
}

func TestResetAfterCrash(t *testing.T) {
	m := NewMachine()
	applyAll(m, journal.PhaseStart)
	m.ResetAfterCrash()
	if m.Status() != Pending {
		t.Fatalf("running step should reset to Pending, got %s", m.Status())
	}
	m2 := NewMachine()
	applyAll(m2, journal.PhaseStart, journal.PhaseSuccess, journal.PhaseCompStart)
	m2.ResetAfterCrash()
	if m2.Status() != Succeeded {
		t.Fatalf("compensating step should reset to Succeeded, got %s", m2.Status())
	}
	// Terminal states are untouched.
	m3 := NewMachine()
	applyAll(m3, journal.PhaseStart, journal.PhaseSuccess)
	m3.ResetAfterCrash()
	if m3.Status() != Succeeded {
		t.Fatalf("succeeded step changed to %s", m3.Status())
	}
}

func TestReplayDeterminism(t *testing.T) {
	recs := []journal.Record{
		{Seq: 1, Phase: journal.PhaseStart, StepID: "s"},
		{Seq: 2, Phase: journal.PhaseRetry, StepID: "s"},
		{Seq: 3, Phase: journal.PhaseSuccess, StepID: "s"},
	}
	build := func() *Machine {
		m := NewMachine()
		for _, r := range recs {
			m.Apply(r)
		}
		return m
	}
	a, b := build(), build()
	if a.Status() != b.Status() {
		t.Fatal("status mismatch")
	}
	ea, ca := a.Counts()
	eb, cb := b.Counts()
	if ea != eb || ca != cb {
		t.Fatal("count mismatch")
	}
}
