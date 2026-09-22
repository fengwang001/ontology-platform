package orchestrate

import "ontology/step"

// Query returns a consistent, immutable snapshot. It never advances state;
// two queries with no mutation between them return equal values.
func (o *Orchestrator) Query() Snapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.snapshotLocked()
}

func (o *Orchestrator) snapshotLocked() Snapshot {
	visible := o.phase
	s := Snapshot{
		Phase:       visible,
		Outcome:     visible,
		JournalLen:  o.j.Len(),
		MaxConcSeen: o.maxConc,
		Failure:     o.failure,
	}
	s.Steps = make([]StepView, 0, len(o.machines))
	for _, id := range o.g.Nodes() {
		m := o.machines[id]
		if m == nil {
			s.Steps = append(s.Steps, StepView{ID: id})
			continue
		}
		// Hide intermediate compensation states from external queries until
		// the whole compensation sequence has finished (atomic two-outcome).
		st := m.State()
		if visible != PhaseAborted && (st == step.Compensating || st == step.Compensated || st == step.CompFailed) {
			st = step.Done
		}
		s.Steps = append(s.Steps, StepView{
			ID:            id,
			State:         st,
			ExecCount:     m.ExecCount(),
			CompCount:     m.CompCount(),
			FailureDetail: m.FailMessage(),
			CompFailure:   m.CompFailMessage(),
		})
	}
	if visible == PhaseAborted {
		s.CompFailures = append([]CompFailure(nil), o.compFails...)
	}
	return s
}

// JournalImage returns the raw storage image for crash/restart tests.
func (o *Orchestrator) JournalImage() []byte {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.j.Bytes()
}

// JournalLen returns the durable record count.
func (o *Orchestrator) JournalLen() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.j.Len()
}

// QueryStep returns the view of one step; unknown ids return a zero view.
func (o *Orchestrator) QueryStep(id string) StepView {
	o.mu.RLock()
	defer o.mu.RUnlock()
	m := o.machines[id]
	if m == nil {
		return StepView{ID: id}
	}
	return StepView{
		ID:        id,
		State:     m.State(),
		ExecCount: m.ExecCount(),
		CompCount: m.CompCount(),
	}
}
