package saga

import (
	"ontology/journal"
	"ontology/step"
)

// buildState reconstructs the full State purely from one instance's records.
func buildState(id string, steps []step.Step, rs []journal.Record) State {
	st := State{Instance: id, Steps: make([]StepState, len(steps))}
	for i := range steps {
		st.Steps[i] = StepState{Index: i, Name: steps[i].Name, IdemKey: steps[i].IdemKey}
	}
	terminalForward, terminalComp := false, false
	for _, r := range rs {
		st.LastRecordSeq = r.Seq
		if r.Index >= 0 && r.Index < len(st.Steps) {
			s := &st.Steps[r.Index]
			switch r.Kind {
			case journal.AttemptForward, journal.AttemptComp:
				st.PhysicalCalls++
			case journal.ForwardOK, journal.ForwardUncertain, journal.ForwardFailed:
				s.Forward = r.Kind
				s.ForwardErr = r.ErrText
				st.LastError = r.ErrText
			case journal.Compensated, journal.CompFailed:
				s.Comp = r.Kind
				s.CompErr = r.ErrText
			}
		}
		if r.Kind == journal.MarkForwardDone {
			terminalForward = true
		}
		if r.Kind == journal.MarkCompDone {
			terminalComp = true
		}
	}
	succeeded, compensated := map[int]bool{}, map[int]bool{}
	for _, s := range st.Steps {
		if s.Forward == journal.ForwardOK || s.Forward == journal.ForwardUncertain {
			succeeded[s.Index] = true
		}
		if s.Comp == journal.Compensated {
			compensated[s.Index] = true
		}
		if s.Comp == journal.CompFailed {
			st.FailedComp = append(st.FailedComp, s.Index)
		}
	}
	switch {
	case terminalComp:
		if len(st.FailedComp) > 0 {
			st.Status = StatusPartialComp
		} else {
			st.Status = StatusCompensated
		}
	case terminalForward:
		st.Status = StatusSucceeded
	case hasForwardFailure(st) || compensationStarted(st):
		st.Status = StatusCompensating
	default:
		st.Status = StatusRunning
	}
	return st
}

func compensationStarted(st State) bool {
	for _, s := range st.Steps {
		if s.Comp != 0 {
			return true
		}
	}
	return false
}

func hasForwardFailure(st State) bool {
	for _, s := range st.Steps {
		if s.Forward == journal.ForwardFailed || s.Forward == journal.ForwardUncertain {
			return true
		}
	}
	return false
}

// State returns the online state of a registered instance, rebuilt from the
// journal so it is identical to Replay.
func (o *Orchestrator) State(id string) (State, error) {
	o.mu.Lock()
	steps, ok := o.reg[id]
	o.mu.Unlock()
	if !ok {
		return State{}, ErrInstanceNotFound
	}
	return buildState(id, steps.def, o.j.Read(id)), nil
}

// Replay reconstructs state from a journal alone; it is the exported pure
// form of the same reconstruction the online path uses.
func Replay(id string, steps []step.Step, j *journal.Journal) State {
	return buildState(id, steps, j.Read(id))
}
