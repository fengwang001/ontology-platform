package let

// gcd returns the greatest common divisor of a and b (both positive).
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// lcm returns the least common multiple of a and b (both positive).
func lcm(a, b int) int {
	return a / gcd(a, b) * b
}

// hyperperiod returns H, the lcm of the chain's periods.
func hyperperiod(ts []Task) int {
	h := 1
	for _, t := range ts {
		h = lcm(h, t.T)
	}
	return h
}

// maxPhase returns Phi, the maximum phase on the chain.
func maxPhase(ts []Task) int {
	m := 0
	for _, t := range ts {
		if t.Phi > m {
			m = t.Phi
		}
	}
	return m
}

// sumPeriods returns the sum of the chain's periods.
func sumPeriods(ts []Task) int {
	s := 0
	for _, t := range ts {
		s += t.T
	}
	return s
}

// ceilDiv returns ceil(a/b) for b > 0.
func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// floorDiv returns floor(a/b) for b > 0, also for negative a.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// firstReleaseGE returns the first release time phi+j*period that is >= tm.
func firstReleaseGE(phi, period, tm int) int {
	if tm <= phi {
		return phi
	}
	return phi + ceilDiv(tm-phi, period)*period
}

// reactionAt computes Reaction(x): tau1 takes its first job released at or
// after x; each subsequent task takes its first job released at or after
// the predecessor's write time (same-instant visibility). The result is
// the final write time minus x.
func reactionAt(ts []Task, x int) int {
	tm := x
	for _, task := range ts {
		r := firstReleaseGE(task.Phi, task.T, tm)
		tm = r + task.W
	}
	return tm - x
}

// ageAt computes Age(x): taun takes its last job written at or before x;
// walking backwards, each task takes its last job written at or before the
// successor's chosen release. The result is x minus tau1's release.
func ageAt(ts []Task, x int) int {
	u := x
	for k := len(ts) - 1; k >= 0; k-- {
		task := ts[k]
		j := floorDiv(u-task.W-task.Phi, task.T)
		u = task.Phi + j*task.T
	}
	return x - u
}

// analyze computes MaxReaction, MinReaction and MaxAge of a chain.
// Reaction(x) and Age(x) are H-periodic, so it suffices to sweep one
// hyperperiod: x in [Phi, Phi+H) for reaction, x in [W, W+H) with
// W = Phi + 2*sum(T) for age (far enough from time 0 that every backward
// walk finds real jobs).
func analyze(ts []Task) Result {
	phi := maxPhase(ts)
	h := hyperperiod(ts)
	var res Result
	res.MinReaction = int(^uint(0) >> 1)
	for x := phi; x < phi+h; x++ {
		r := reactionAt(ts, x)
		if r > res.MaxReaction {
			res.MaxReaction = r
		}
		if r < res.MinReaction {
			res.MinReaction = r
		}
	}
	w := phi + 2*sumPeriods(ts)
	for x := w; x < w+h; x++ {
		if a := ageAt(ts, x); a > res.MaxAge {
			res.MaxAge = a
		}
	}
	return res
}

// Analyze returns the MaxReaction, MinReaction and MaxAge of a chain.
// The result depends only on the current task parameters, not on call
// order, and repeated calls with the same parameters agree.
func (a *Analyzer) Analyze(name string) (Result, error) {
	if name == "" {
		return Result{}, ErrInvalidArgument
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids, ok := a.chains[name]
	if !ok {
		return Result{}, ErrNotFound
	}
	return analyze(a.chainTasksLocked(ids)), nil
}

// Tune searches phases of tau2..taun (tau1's phase is fixed) for the
// combination minimizing MaxReaction. Ties are broken by choosing the
// lexicographically smallest phase vector (phi2, ..., phin). The new
// phases are committed only if the best MaxReaction is strictly smaller
// than the current one; otherwise nothing changes and the returned
// "after" values equal the "before" values. Tune never changes write
// delays or chain composition.
func (a *Analyzer) Tune(name string) (TuneResult, error) {
	if name == "" {
		return TuneResult{}, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ids, ok := a.chains[name]
	if !ok {
		return TuneResult{}, ErrNotFound
	}
	ts := a.chainTasksLocked(ids)
	if hyperperiod(ts) > MaxHyperperiod {
		return TuneResult{}, ErrTooLarge
	}
	product := 1
	for _, t := range ts[1:] {
		product *= t.T
	}
	if product > MaxTuneProduct {
		return TuneResult{}, ErrTooLarge
	}
	inChain := make(map[string]bool, len(ids))
	for _, id := range ids {
		inChain[id] = true
	}
	for other, oids := range a.chains {
		if other == name {
			continue
		}
		for _, id := range oids {
			if inChain[id] {
				return TuneResult{}, ErrShared
			}
		}
	}

	current := analyze(ts)
	res := TuneResult{
		MaxReactionBefore: current.MaxReaction,
		MaxReactionAfter:  current.MaxReaction,
		PhasesBefore:      phasesOf(ts),
		PhasesAfter:       phasesOf(ts),
	}

	// Enumerate phase vectors (phi2..phin) in lexicographic order with an
	// odometer; keeping the first vector achieving the best value yields
	// the lexicographically smallest one.
	n := len(ts)
	best := current.MaxReaction
	var bestVec []int
	vec := make([]int, n) // vec[k] is the trial phase of task k; vec[0] unused
	for {
		trial := make([]Task, n)
		copy(trial, ts)
		for k := 1; k < n; k++ {
			trial[k].Phi = vec[k]
		}
		if m := analyze(trial).MaxReaction; m < best {
			best = m
			bestVec = append([]int(nil), vec[1:]...)
		}
		k := n - 1
		for k >= 1 {
			vec[k]++
			if vec[k] < ts[k].T {
				break
			}
			vec[k] = 0
			k--
		}
		if k < 1 {
			break
		}
	}

	if best < current.MaxReaction {
		for k := 1; k < n; k++ {
			a.tasks[ids[k]].Phi = bestVec[k-1]
			ts[k].Phi = bestVec[k-1]
		}
		res.Changed = true
		res.MaxReactionAfter = best
		res.PhasesAfter = phasesOf(ts)
	}
	return res, nil
}

// phasesOf returns the phase vector (phi1, ..., phin) of a chain.
func phasesOf(ts []Task) []int {
	p := make([]int, len(ts))
	for i, t := range ts {
		p[i] = t.Phi
	}
	return p
}
