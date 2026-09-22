package orchestrate

import (
	"sync"

	"ontology/step"
)

// Run drives the workflow to a terminal outcome. Recovered runs continue
// from exactly the journal state: completed steps are never re-executed.
func (o *Orchestrator) Run() (snap Snapshot, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ce, ok := r.(*CrashedError); ok {
				err = ce
				snap = o.snapshotLocked()
				return
			}
			panic(r)
		}
	}()

	o.mu.Lock()
	if o.terminal {
		snap = o.snapshotLocked()
		o.mu.Unlock()
		return snap, nil
	}
	for _, id := range o.g.Nodes() {
		if _, ok := o.actions[id]; !ok {
			o.mu.Unlock()
			return Snapshot{}, ErrUnregistered
		}
	}
	o.phase = PhaseExecuting
	o.mu.Unlock()

	failID := o.executeLayers()
	if failID != "" {
		o.compensateAll(failID)
	}

	o.mu.Lock()
	o.terminal = true
	if failID == "" {
		o.phase = PhaseCompleted
	}
	snap = o.snapshotLocked()
	o.mu.Unlock()
	return snap, nil
}

// executeLayers schedules layer by layer; steps inside one layer run
// concurrently (bounded by MaxConcurrency). It returns the id of the step
// whose terminal failure aborts the run, or "" on full success.
func (o *Orchestrator) executeLayers() string {
	sem := make(chan struct{}, o.effectiveConcurrency())
	for _, layer := range o.g.Layers() {
		var wg sync.WaitGroup
		var failMu sync.Mutex
		failID := ""
		for _, id := range layer {
			o.mu.RLock()
			st := o.machines[id].State()
			o.mu.RUnlock()
			if st == step.Done || st == step.Compensated {
				continue // recovered: never re-run
			}
			if st == step.Failed {
				failMu.Lock()
				if failID == "" {
					failID = id
				}
				failMu.Unlock()
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(sid string) {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						o.crashMu.Lock()
						o.crash = r
						o.crashMu.Unlock()
					}
				}()
				o.trackConcurrencyEnter()
				defer o.trackConcurrencyLeave()
				if ferr := o.runOneWithRetries(sid); ferr {
					failMu.Lock()
					if failID == "" {
						failID = sid
					}
					failMu.Unlock()
				}
			}(id)
		}
		wg.Wait()
		o.crashMu.Lock()
		crash := o.crash
		o.crashMu.Unlock()
		if crash != nil {
			panic(crash)
		}
		if failID != "" {
			return failID
		}
	}
	return ""
}

// runOneWithRetries performs attempt 1..N+1. It returns true on terminal
// failure. A recovered in-flight attempt is resumed without a new start
// record; later retry attempts record starts normally.
func (o *Orchestrator) runOneWithRetries(id string) bool {
	m := o.machines[id]
	act := o.actions[id]

	// If recovery left this step Running, re-drive the dangling attempt.
	o.mu.RLock()
	resume := m.State() == step.Running
	o.mu.RUnlock()

	var execErr error
	if resume {
		execErr = o.ex.ResumeExecute(m, act)
	} else {
		execErr = o.ex.RunExecute(m, act)
	}
	if execErr == nil {
		return false
	}

	attempts := m.ExecCount()
	for o.cfg.Policy.RetryAllowed(attempts-1, execErr) {
		wait := o.cfg.Policy.Backoff(attempts + 1)
		if wait > 0 {
			o.cfg.Clock.Sleep(wait)
		}
		execErr = o.ex.RunExecute(m, act)
		attempts = m.ExecCount()
		if execErr == nil {
			return false
		}
	}
	_ = o.ex.MarkFailed(m, execErr)
	o.mu.Lock()
	o.failure = execErr.Error()
	o.mu.Unlock()
	return true
}

func (o *Orchestrator) effectiveConcurrency() int {
	if n := o.cfg.MaxConcurrency; n > 0 {
		return n
	}
	if w := o.g.MaxWidth(); w > 0 {
		return w
	}
	return 1
}

func (o *Orchestrator) trackConcurrencyEnter() {
	o.mu.Lock()
	o.activeRunners++
	if o.activeRunners > o.maxConc {
		o.maxConc = o.activeRunners
	}
	o.mu.Unlock()
}

func (o *Orchestrator) trackConcurrencyLeave() {
	o.mu.Lock()
	o.activeRunners--
	o.mu.Unlock()
}
