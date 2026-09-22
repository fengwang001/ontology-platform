package orchestrate

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/journal"
	"ontology/step"
)

// Run executes the workflow to a terminal phase: PhaseCompleted when
// every step succeeds, PhaseCompensated when any step fails terminally
// (all succeeded steps are then compensated in reverse topological
// order). There is no other externally visible terminal state.
func (o *Orchestrator) Run(ctx context.Context) (err error) {
	defer recoverCrash(&err)
	if err := o.checkLimits(); err != nil {
		return err
	}
	o.mu.Lock()
	o.phase = PhaseRunning
	o.mu.Unlock()

	if o.needsCompensation {
		return o.failRecovered(o.compensate(ctx))
	}
	for _, layer := range o.layers {
		failed, lerr := o.execLayer(ctx, layer)
		if lerr != nil {
			return lerr // crash or journal exhaustion
		}
		if failed != nil {
			cerr := o.compensate(ctx)
			return &FailureError{Step: failed.id, Err: failed.err, Compensation: cerr}
		}
	}
	o.mu.Lock()
	o.phase = PhaseCompleted
	o.mu.Unlock()
	return nil
}

type stepFailure struct {
	id  string
	err error
}

// execLayer runs every Pending step of one layer concurrently, bounded by
// the concurrency semaphore. A slow step never blocks its independent
// siblings: each step gets its own goroutine.
func (o *Orchestrator) execLayer(ctx context.Context, layer []string) (*stepFailure, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first *stepFailure
	var crashed atomic.Bool
	for _, id := range layer {
		if o.mach[id].Status() != step.Pending {
			continue // already done in an earlier life: never re-run
		}
		def, ok := o.defs[id]
		if !ok {
			mu.Lock()
			if first == nil {
				first = &stepFailure{id, fmt.Errorf("orchestrate: no step registered for %q", id)}
			}
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					if !isCrash(r) {
						panic(r)
					}
					crashed.Store(true)
					cancel()
				}
			}()
			o.sem <- struct{}{}
			defer func() { <-o.sem }()
			n := o.inflight.Add(1)
			for {
				old := o.maxInflight.Load()
				if n <= old || o.maxInflight.CompareAndSwap(old, n) {
					break
				}
			}
			defer o.inflight.Add(-1)
			if err := o.execStep(ctx, id, def); err != nil {
				mu.Lock()
				if first == nil {
					first = &stepFailure{id, err}
				}
				mu.Unlock()
				cancel()
			}
		}()
	}
	wg.Wait()
	if crashed.Load() {
		return nil, ErrCrashed
	}
	return first, nil
}

// execStep runs one step with policy-driven retries, journaling every
// transition before it becomes visible in the machine.
func (o *Orchestrator) execStep(ctx context.Context, id string, def step.Step) error {
	m := o.mach[id]
	if err := o.append(journal.PhaseStart, id, ""); err != nil {
		return err
	}
	for {
		runErr := def.Run(ctx)
		if runErr == nil {
			return o.append(journal.PhaseSuccess, id, "")
		}
		if !o.pol.AllowRetry(m, runErr) {
			if aerr := o.append(journal.PhaseFailure, id, runErr.Error()); aerr != nil {
				return aerr
			}
			return runErr
		}
		if err := o.append(journal.PhaseRetry, id, runErr.Error()); err != nil {
			return err
		}
		o.clock.Sleep(o.pol.Wait(failedAttempts(m)))
	}
}

// failedAttempts returns how many attempts have failed so far. It is the
// journal exec count minus one, because the Retry record for the next
// attempt is appended before the backoff sleep.
func failedAttempts(m *step.Machine) int {
	exec, _ := m.Counts()
	return exec - 1
}

// append writes one journal record and folds it into the step machine,
// keeping the machine a pure function of the trail.
func (o *Orchestrator) append(phase journal.Phase, id, note string) error {
	rec, err := o.j.Append(phase, id, note)
	if err != nil {
		return err
	}
	o.mach[id].Apply(rec)
	return nil
}

func isCrash(r any) bool {
	_, ok := r.(CrashSignal)
	if ok {
		return true
	}
	_, ok = r.(*CrashSignal)
	return ok
}

func recoverCrash(err *error) {
	if r := recover(); r != nil {
		if !isCrash(r) {
			panic(r)
		}
		*err = ErrCrashed
	}
}
