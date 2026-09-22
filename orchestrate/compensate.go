package orchestrate

import "ontology/step"

// compensateAll runs compensation in strict reverse topological order and
// strictly serially: compensation of B finishes before compensation of A
// even starts. The failed step itself and never-started steps are skipped.
// Compensation failures are aggregated and never stop the remaining steps.
//
// Atomicity: phase stays PhaseExecuting for all external queries until the
// whole sequence is finished, then flips to PhaseAborted in one critical
// section (rule 8).
func (o *Orchestrator) compensateAll(failedID string) {
	for _, id := range o.g.ReverseTopoOrder() {
		m := o.machines[id]
		st := m.State()
		switch st {
		case step.Done, step.Compensating:
			// eligible: executed successfully at least once
		default:
			continue // Pending/Running/Failed/Compensated/CompFailed
		}
		o.compensateOne(id)
	}
	o.mu.Lock()
	o.phase = PhaseAborted
	o.mu.Unlock()
}

func (o *Orchestrator) compensateOne(id string) {
	m := o.machines[id]
	act := o.actions[id]
	resume := m.State() == step.Compensating

	var cerr error
	if resume {
		cerr = o.ex.ResumeCompensate(m, act)
	} else {
		cerr = o.ex.RunCompensate(m, act)
	}
	if cerr == nil {
		return
	}
	attempts := m.CompCount()
	for o.cfg.Policy.CompRetryAllowed(attempts, cerr) {
		wait := o.cfg.Policy.CompBackoff(attempts + 1)
		if wait > 0 {
			o.cfg.Clock.Sleep(wait)
		}
		cerr = o.ex.RunCompensate(m, act) // appends a fresh start after CompFailed
		attempts = m.CompCount()
		if cerr == nil {
			return
		}
	}
	o.recordCompFailure(id, cerr)
}

func (o *Orchestrator) recordCompFailure(id string, cerr error) {
	o.mu.Lock()
	o.compFails = append(o.compFails, CompFailure{StepID: id, Detail: cerr.Error()})
	o.mu.Unlock()
}
