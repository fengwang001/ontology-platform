package rollback

// Rollback executes every registered compensation strictly in reverse step
// order. Guarantees:
//
//   - Each compensation runs exactly once, even when Rollback is invoked
//     concurrently from multiple goroutines; concurrent callers receive the
//     very same result.
//   - A failing (or panicking) compensation does not stop the remaining
//     compensations from running.
//   - All compensation failures are returned together as *AggregateError,
//     keyed by step number.
//   - Any failed compensation poisons the store; the earliest such step is
//     the pollution point.
//   - Rollback after a successful commit is rejected with ErrCommitted and
//     never replays compensations.
//
// A rollback over a zero-step unit is a no-op that returns nil.
func (u *Unit) Rollback() error {
	u.mu.Lock()
	for u.state == stateRolling {
		u.rollDone.Wait()
	}

	if u.state == stateCommitted {
		u.mu.Unlock()
		return ErrCommitted
	}
	if u.state == stateRolledBack {
		err := u.rollResult
		u.mu.Unlock()
		return err
	}

	// Snapshot only steps whose actions succeeded. The failing step's entry
	// was removed by Step before calling Rollback.
	snapshot := make([]registered, len(u.steps))
	copy(snapshot, u.steps)

	u.state = stateRolling
	u.mu.Unlock()

	failures := make(map[int]error)

	for i := len(snapshot) - 1; i >= 0; i-- {
		stepNo := i + 1
		u.runCompensation(stepNo, snapshot[i].compensation, failures)
	}

	u.mu.Lock()
	u.traced = traceOf(snapshot)

	var result error
	if len(failures) > 0 {
		earliest := len(snapshot)
		for step := range failures {
			if step < earliest {
				earliest = step
			}
		}
		// Earliest pollution point = the failed compensation that, in forward
		// order, happened first (smallest step number).
		u.store.MarkPolluted(earliest)
		result = newAggregateError(failures)
	}

	u.rollResult = result
	u.state = stateRolledBack
	u.rollDone.Broadcast()
	u.mu.Unlock()

	return result
}

func (u *Unit) runCompensation(step int, compensation Compensation, failures map[int]error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failures[step] = &CompensationPanic{Step: step, Value: recovered}
		}
	}()

	if err := compensation(); err != nil {
		failures[step] = err
	}
}

// traceOf returns the step numbers of snapshot in compensation (reverse)
// order.
func traceOf(snapshot []registered) []int {
	trace := make([]int, 0, len(snapshot))
	for i := len(snapshot) - 1; i >= 0; i-- {
		trace = append(trace, i+1)
	}
	return trace
}
