package orchestrate

import "ontology/journal"

// CrashTarget selects exactly one physical write boundary: the Append that
// receives sequence number Seq, at boundary Point.
type CrashTarget struct {
	Seq   int
	Point journal.CrashPoint
}

// CrashHookAt builds a hook that panics with *CrashedError once, at the
// first Append whose assigned sequence equals t.Seq and boundary equals
// t.Point. Earlier/later boundaries are left untouched.
func CrashHookAt(t CrashTarget) journal.CrashHook {
	fired := false
	return func(r journal.Record, p journal.CrashPoint) {
		if fired || r.Seq != t.Seq || p != t.Point {
			return
		}
		fired = true
		panic(&CrashedError{Seq: r.Seq, Point: p})
	}
}

// MaxObservedConcurrency returns the peak number of concurrently executing
// step goroutines observed during the run.
func (o *Orchestrator) MaxObservedConcurrency() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.maxConc
}
