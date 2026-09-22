package saga

import (
	"context"

	"ontology/compens"
	"ontology/journal"
	"ontology/step"
)

func (o *Orchestrator) rec(id string, k journal.Kind, idx, attempt int, key, errText string) error {
	_, err := o.j.Append(journal.Record{
		Instance: id, Kind: k, Index: idx, Key: key,
		Attempt: attempt, ErrText: errText, Ts: o.cfg.Now(),
	})
	return err
}

// execute runs a brand new instance: no prior records may exist.
func (o *Orchestrator) execute(ctx context.Context, id string, steps []step.Step) error {
	return o.executeFrom(ctx, id, steps, nil)
}

// executeFrom reconstructs progress from records and continues exactly where
// the log ends; already-terminal steps are never invoked again.
func (o *Orchestrator) executeFrom(ctx context.Context, id string, steps []step.Step, rs []journal.Record) error {
	st := buildState(id, steps, rs)
	for st.Status == StatusRunning {
		i := nextForward(st)
		if i >= len(steps) {
			if err := o.rec(id, journal.MarkForwardDone, -1, 0, "", ""); err != nil {
				return err
			}
			return nil
		}
		if err := o.runForward(ctx, id, steps[i], i); err != nil {
			return err
		}
		rs = o.j.Read(id)
		st = buildState(id, steps, rs)
	}
	return o.runCompensation(ctx, id, steps, st)
}

// runForward physically invokes one step's forward action and records exactly
// one terminal forward record. Unknown errors stop retries immediately.
func (o *Orchestrator) runForward(ctx context.Context, id string, st0 step.Step, idx int) error {
	lastErr := error(nil)
	for attempt := 0; attempt <= st0.Retries; attempt++ {
		if err := o.rec(id, journal.AttemptForward, idx, attempt, st0.IdemKey, ""); err != nil {
			return err
		}
		err := st0.Do(ctx, st0.IdemKey, attempt, o.cfg.Now())
		if err == nil {
			return o.rec(id, journal.ForwardOK, idx, attempt, st0.IdemKey, "")
		}
		lastErr = err
		if !step.IsDefinite(err) {
			return o.rec(id, journal.ForwardUncertain, idx, attempt, st0.IdemKey, err.Error())
		}
	}
	return o.rec(id, journal.ForwardFailed, idx, st0.Retries, st0.IdemKey, lastErr.Error())
}

// runCompensation compensates succeeded steps in strict reverse order. A
// compensation failure is recorded but never aborts the remaining steps.
func (o *Orchestrator) runCompensation(ctx context.Context, id string, steps []step.Step, st State) error {
	succeeded := map[int]bool{}
	done := map[int]bool{}
	for _, s := range st.Steps {
		if s.Forward == journal.ForwardOK || s.Forward == journal.ForwardUncertain {
			succeeded[s.Index] = true
		}
		if s.Comp == journal.Compensated {
			done[s.Index] = true
		}
	}
	// The succeeded set already excludes the failed step and never-executed
	// steps; the plan is strictly reverse ordered.
	for _, it := range compens.Pending(steps, succeeded, len(steps)-1, done).Items {
		if err := o.runOneCompensation(ctx, id, it.Step, it.Index); err != nil {
			return err
		}
	}
	return o.rec(id, journal.MarkCompDone, -1, 0, "", "")
}

func (o *Orchestrator) runOneCompensation(ctx context.Context, id string, st0 step.Step, idx int) error {
	if st0.Compensate == nil {
		e := &NilCompensationError{Index: idx, IdemKey: st0.IdemKey}
		return o.rec(id, journal.CompFailed, idx, 0, st0.IdemKey, e.Error())
	}
	lastErr := error(nil)
	for attempt := 0; attempt <= st0.Retries; attempt++ {
		if err := o.rec(id, journal.AttemptComp, idx, attempt, st0.IdemKey, ""); err != nil {
			return err
		}
		err := st0.Compensate(ctx, st0.IdemKey, attempt, o.cfg.Now())
		if err == nil {
			return o.rec(id, journal.Compensated, idx, attempt, st0.IdemKey, "")
		}
		lastErr = err
		if !step.IsDefinite(err) {
			break
		}
	}
	return o.rec(id, journal.CompFailed, idx, st0.Retries, st0.IdemKey, lastErr.Error())
}

func nextForward(st State) int {
	for _, s := range st.Steps {
		if s.Forward == 0 {
			return s.Index
		}
		if s.Forward == journal.ForwardFailed || s.Forward == journal.ForwardUncertain {
			return len(st.Steps)
		}
	}
	return len(st.Steps)
}
