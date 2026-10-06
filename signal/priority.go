package signal

// Priority handling: emergency preemption, bus adjustment, and the
// adjacent-cycle skip registry.

func (e *engine) preemptBuses() {
	for _, r := range e.requests {
		if r.Kind == ReqBus && r.State == StateApplied {
			r.State = StatePreempted
		}
	}
}

func (e *engine) completeBuses() {
	for _, r := range e.requests {
		if r.Kind == ReqBus && r.State == StateApplied {
			r.State = StateCompleted
		}
	}
}

// pumpQueue starts the head emergency request when nothing is serving.
func (e *engine) pumpQueue(s int) {
	for e.serving == nil && len(e.queue) > 0 {
		r := e.queue[0]
		e.queue = e.queue[1:]
		e.startService(r, s)
	}
}

// startService begins serving an emergency request at time s. Any applied
// bus adjustment on the current phase is overridden (preempted).
func (e *engine) startService(r *Request, s int) {
	e.preemptBuses()
	r.State = StateServing
	e.serving = r
	e.atWrap = false
	p := e.phase
	if p == r.Target {
		// Already on target: extend the green to MaxGreen.
		ge := e.phaseStart + e.specs[p].MaxGreen
		if ge < s {
			ge = s
		}
		e.greenEnd = ge
		return
	}
	// Otherwise finish the current phase at its minimum green, run its
	// clearance, then jump straight to the target phase.
	e1 := e.phaseStart + e.specs[p].MinGreen
	if e1 < s {
		e1 = s
	}
	e.greenEnd = e1
	e.pendingJump = r.Target
	e.jumpHold = true
}

// computeSkips returns cycleIdx -> skipped phases for a jump from cur to
// target initiated in cycle k. A jump to an earlier phase crosses the cycle
// boundary, so leading phases fall into cycle k+1.
func computeSkips(cur, target, k, n int) map[int]map[int]bool {
	out := map[int]map[int]bool{}
	add := func(c, p int) {
		if out[c] == nil {
			out[c] = map[int]bool{}
		}
		out[c][p] = true
	}
	if target > cur {
		for p := cur + 1; p < target; p++ {
			add(k, p)
		}
	} else if target < cur {
		for p := cur + 1; p < n; p++ {
			add(k, p)
		}
		for p := 0; p < target; p++ {
			add(k+1, p)
		}
	}
	return out
}

// violatesSkips reports whether any phase would be skipped in two adjacent
// cycles.
func violatesSkips(skipped map[int]map[int]bool, sk map[int]map[int]bool) bool {
	for c, ps := range sk {
		for p := range ps {
			if skipped[c-1][p] || skipped[c+1][p] {
				return true
			}
		}
	}
	return false
}

func registerSkips(skipped map[int]map[int]bool, sk map[int]map[int]bool) {
	for c, ps := range sk {
		if skipped[c] == nil {
			skipped[c] = map[int]bool{}
		}
		for p := range ps {
			skipped[c][p] = true
		}
	}
}
