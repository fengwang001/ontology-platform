package orchestrate

import (
	"context"
	"errors"

	"ontology/journal"
	"ontology/step"
)

// compensate undoes every succeeded step in reverse topological order,
// sequentially: a step's compensation fully completes before its
// dependencies' compensations start. A failing compensation never stops
// the others; all failures are aggregated. The whole critical section
// holds o.mu so queries never observe a half-compensated state.
func (o *Orchestrator) compensate(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	failures := map[string]error{}
	for _, id := range o.rev {
		m := o.mach[id]
		switch m.Status() {
		case step.Succeeded:
			// compensate below
		case step.CompensateFailed:
			failures[id] = errors.New(m.Note())
			continue
		default:
			continue // failed, pending, or already compensated: skip
		}
		if err := o.compensateOne(ctx, id, m); err != nil {
			failures[id] = err
		}
	}
	o.phase = PhaseCompensated
	if len(failures) > 0 {
		return &CompensationError{Failures: failures}
	}
	return nil
}

func (o *Orchestrator) compensateOne(ctx context.Context, id string, m *step.Machine) error {
	def := o.defs[id]
	for {
		if err := o.append(journal.PhaseCompStart, id, ""); err != nil {
			return err
		}
		var cerr error
		if def.Compensate != nil {
			cerr = def.Compensate(ctx)
		}
		if cerr == nil {
			return o.append(journal.PhaseCompOK, id, "")
		}
		_, comp := m.Counts()
		if !o.pol.AllowCompensateRetry(comp) {
			if aerr := o.append(journal.PhaseCompFail, id, cerr.Error()); aerr != nil {
				return aerr
			}
			return cerr
		}
		o.clock.Sleep(o.pol.Wait(comp))
	}
}

// failRecovered builds the terminal error for a run that resumed into
// compensation after a crash.
func (o *Orchestrator) failRecovered(cerr error) error {
	for _, id := range o.topo {
		if o.mach[id].Status() == step.Failed {
			return &FailureError{
				Step:         id,
				Err:          errors.New(o.mach[id].Note()),
				Compensation: cerr,
			}
		}
	}
	return cerr
}
