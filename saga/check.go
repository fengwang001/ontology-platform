package saga

import (
	"errors"
	"fmt"

	"ontology/journal"
	"ontology/step"
)

// SelfCheck validates every registered instance: its record sequence must be
// legal, and its online state must equal state rebuilt from the journal alone.
func (o *Orchestrator) SelfCheck() error {
	o.mu.Lock()
	ids := make([]string, 0, len(o.reg))
	defs := map[string][]step.Step{}
	for id, in := range o.reg {
		ids = append(ids, id)
		defs[id] = in.def
	}
	o.mu.Unlock()
	for _, id := range ids {
		rs := o.j.Read(id)
		if err := validateSequence(id, defs[id], rs); err != nil {
			return err
		}
		if !statesEqual(buildState(id, defs[id], rs), Replay(id, defs[id], o.j)) {
			return fmt.Errorf("saga: state divergence for %s", id)
		}
	}
	return nil
}

// validateSequence rejects illegal record ordering for one instance.
func validateSequence(id string, steps []step.Step, rs []journal.Record) error {
	type slot struct {
		fwd, comp journal.Kind
		seenF     bool
	}
	slots := make([]slot, len(steps))
	marked := false
	for _, r := range rs {
		if r.Instance != id {
			return fmt.Errorf("journal: foreign record in %s", id)
		}
		if r.Kind == journal.MarkForwardDone || r.Kind == journal.MarkCompDone {
			if marked {
				return errors.New("journal: duplicate terminal marker")
			}
			marked = true
			continue
		}
		if marked || r.Index < 0 || r.Index >= len(steps) {
			return errors.New("journal: record after terminal marker or bad index")
		}
		s := &slots[r.Index]
		switch r.Kind {
		case journal.AttemptForward:
			if s.fwd != 0 || s.comp != 0 {
				return errors.New("journal: forward attempt after terminal state")
			}
		case journal.ForwardOK, journal.ForwardUncertain, journal.ForwardFailed:
			if s.seenF {
				return errors.New("journal: two forward terminals for one step")
			}
			s.seenF, s.fwd = true, r.Kind
		case journal.AttemptComp, journal.Compensated, journal.CompFailed:
			if !s.seenF || (s.fwd != journal.ForwardOK && s.fwd != journal.ForwardUncertain) {
				return errors.New("journal: compensation before an effective forward")
			}
			if s.comp != 0 {
				return errors.New("journal: compensation terminal recorded twice")
			}
			if r.Kind == journal.Compensated || r.Kind == journal.CompFailed {
				s.comp = r.Kind
			}
		}
	}
	return nil
}

func statesEqual(a, b State) bool {
	if a.Instance != b.Instance || a.Status != b.Status || a.PhysicalCalls != b.PhysicalCalls ||
		a.LastRecordSeq != b.LastRecordSeq || len(a.Steps) != len(b.Steps) ||
		len(a.FailedComp) != len(b.FailedComp) {
		return false
	}
	for i := range a.Steps {
		if a.Steps[i] != b.Steps[i] {
			return false
		}
	}
	for i := range a.FailedComp {
		if a.FailedComp[i] != b.FailedComp[i] {
			return false
		}
	}
	return true
}
