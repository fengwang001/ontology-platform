package demand_test

// This file holds an independent, deliberately naive reference model of
// the specification. It keeps the FULL report history, recomputes window
// energies by summing interval overlaps, enumerates cut subsets by brute
// force, and recomputes the peak from scratch after every report. The
// randomized cross-check in random_test.go verifies that the optimized
// controller agrees with this model on every step.

import (
	"fmt"
	"math"
	"slices"

	"ontology/demand"
)

type naiveReport struct {
	t      int64
	energy float64
}

type naiveLoad struct {
	spec      demand.LoadSpec
	connected bool
	since     int64
	locked    bool
}

type naiveAction struct {
	restore bool
	id      int
}

type naiveResult struct {
	actions        []naiveAction
	stillViolating bool
}

type naive struct {
	cfg     demand.Config
	reports []naiveReport
	loads   map[int]*naiveLoad
	// decision explains the last evaluation in human-readable form and
	// is printed by the randomized test as the decision basis.
	decision string
}

func newNaive(cfg demand.Config) *naive {
	return &naive{cfg: cfg, loads: make(map[int]*naiveLoad)}
}

func (n *naive) now() int64 {
	if len(n.reports) == 0 {
		return 0
	}
	return n.reports[len(n.reports)-1].t
}

func (n *naive) addLoad(spec demand.LoadSpec) error {
	if spec.ID <= 0 || spec.RatedPowerKW <= 0 || spec.Priority < 0 || spec.MinOnSeconds < 0 || spec.MinOffSeconds < 0 {
		return demand.ErrInvalidParam
	}
	if _, ok := n.loads[spec.ID]; ok {
		return demand.ErrStateNotAllowed
	}
	n.loads[spec.ID] = &naiveLoad{spec: spec, connected: true, since: n.now()}
	return nil
}

func (n *naive) removeLoad(id int) error {
	if id <= 0 {
		return demand.ErrInvalidParam
	}
	if _, ok := n.loads[id]; !ok {
		return demand.ErrLoadNotFound
	}
	return demand.ErrStateNotAllowed
}

func (n *naive) lock(id int) error {
	if id <= 0 {
		return demand.ErrInvalidParam
	}
	l, ok := n.loads[id]
	if !ok {
		return demand.ErrLoadNotFound
	}
	if l.locked {
		return demand.ErrStateNotAllowed
	}
	l.locked = true
	return nil
}

func (n *naive) unlock(id int) error {
	if id <= 0 {
		return demand.ErrInvalidParam
	}
	l, ok := n.loads[id]
	if !ok {
		return demand.ErrLoadNotFound
	}
	if !l.locked {
		return demand.ErrStateNotAllowed
	}
	l.locked = false
	return nil
}

// energyBetween sums the energy consumed inside [a, b] by walking the
// full report history; usage before the first report is zero.
func (n *naive) energyBetween(a, b int64) float64 {
	total := 0.0
	for i := 1; i < len(n.reports); i++ {
		lo := max(a, n.reports[i-1].t)
		hi := min(b, n.reports[i].t)
		if hi > lo {
			dt := n.reports[i].t - n.reports[i-1].t
			total += n.reports[i].energy / float64(dt) * float64(hi-lo)
		}
	}
	return total
}

func (n *naive) report(t int64, energy float64) (naiveResult, error) {
	if t < 0 {
		return naiveResult{}, demand.ErrInvalidParam
	}
	if math.IsNaN(energy) || energy < 0 {
		return naiveResult{}, demand.ErrInvalidParam
	}
	if len(n.reports) > 0 && t <= n.now() {
		if energy > 0 {
			return naiveResult{}, demand.ErrDataIllegal
		}
		return naiveResult{}, demand.ErrTimeRegression
	}
	if len(n.reports) > 0 {
		dt := t - n.now()
		if energy/float64(dt) > n.cfg.MaxPhysicalPowerKW {
			return naiveResult{}, demand.ErrDataIllegal
		}
	}
	n.reports = append(n.reports, naiveReport{t: t, energy: energy})
	return n.evaluate(), nil
}

func (n *naive) evaluate() naiveResult {
	now := n.now()
	power := 0.0
	if len(n.reports) >= 2 {
		prev := n.reports[len(n.reports)-2]
		last := n.reports[len(n.reports)-1]
		power = last.energy / float64(last.t-prev.t)
	}

	limit := float64(n.cfg.ContractDemandKW) * float64(n.cfg.WindowSeconds)
	bound := math.Inf(1)
	worstEnd := int64(0)
	for end := (now/n.cfg.SlipSeconds + 1) * n.cfg.SlipSeconds; end <= now+n.cfg.WindowSeconds; end += n.cfg.SlipSeconds {
		occurred := n.energyBetween(end-n.cfg.WindowSeconds, now)
		if b := (limit - occurred) / float64(end-now); b < bound {
			bound = b
			worstEnd = end
		}
	}

	if power > bound {
		return n.cutPhase(now, power, bound, worstEnd)
	}
	return n.restorePhase(now, power, bound)
}

func (n *naive) cutPhase(now int64, power, bound float64, worstEnd int64) naiveResult {
	minPri := math.MaxInt
	for _, l := range n.loads {
		minPri = min(minPri, l.spec.Priority)
	}
	var cands []*naiveLoad
	for _, l := range n.loads {
		if !l.connected || l.locked || l.spec.Priority == minPri {
			continue
		}
		if now-l.since < l.spec.MinOnSeconds {
			continue
		}
		cands = append(cands, l)
	}
	slices.SortFunc(cands, func(a, b *naiveLoad) int { return a.spec.ID - b.spec.ID })

	// Brute-force every subset; keep the best by (count, -prioritySum, ids).
	var best []int
	bestPri := 0
	var bestSum int64
	sums := make([]int64, 1<<uint(len(cands)))
	priSums := make([]int, 1<<uint(len(cands)))
	for mask := 1; mask < 1<<uint(len(cands)); mask++ {
		bit := mask & (-mask)
		i := 0
		for bit>>uint(i) != 1 {
			i++
		}
		prev := mask ^ bit
		sums[mask] = sums[prev] + cands[i].spec.RatedPowerKW
		priSums[mask] = priSums[prev] + cands[i].spec.Priority
	}
	for mask := 0; mask < 1<<uint(len(cands)); mask++ {
		newPower := math.Max(0, power-float64(sums[mask]))
		if newPower > bound {
			continue
		}
		var ids []int
		for i, l := range cands {
			if mask&(1<<uint(i)) != 0 {
				ids = append(ids, l.spec.ID)
			}
		}
		if best == nil ||
			len(ids) < len(best) ||
			(len(ids) == len(best) && priSums[mask] > bestPri) ||
			(len(ids) == len(best) && priSums[mask] == bestPri && slices.Compare(ids, best) < 0) {
			best = ids
			bestPri = priSums[mask]
			bestSum = sums[mask]
		}
	}

	chosen := best
	if chosen == nil {
		// No subset suffices: cut everything cuttable.
		for _, l := range cands {
			chosen = append(chosen, l.spec.ID)
		}
		var s int64
		for _, l := range cands {
			s += l.spec.RatedPowerKW
		}
		bestSum = s
	}

	res := naiveResult{}
	for _, id := range chosen {
		l := n.loads[id]
		l.connected = false
		l.since = now
		res.actions = append(res.actions, naiveAction{id: id})
	}
	res.stillViolating = math.Max(0, power-float64(bestSum)) > bound
	n.decision = fmt.Sprintf("cut: P=%g bound=%g(worstEnd=%d) chose=%v stillViolating=%v",
		power, bound, worstEnd, chosen, res.stillViolating)
	return res
}

func (n *naive) restorePhase(now int64, power, bound float64) naiveResult {
	var cands []*naiveLoad
	for _, l := range n.loads {
		if l.connected || now-l.since < l.spec.MinOffSeconds {
			continue
		}
		cands = append(cands, l)
	}
	slices.SortFunc(cands, func(a, b *naiveLoad) int {
		if a.spec.Priority != b.spec.Priority {
			return a.spec.Priority - b.spec.Priority
		}
		return a.spec.ID - b.spec.ID
	})

	res := naiveResult{}
	cur := power
	stoppedAt := 0
	for _, l := range cands {
		if cur+float64(l.spec.RatedPowerKW) > bound {
			stoppedAt = l.spec.ID
			break
		}
		l.connected = true
		l.since = now
		cur += float64(l.spec.RatedPowerKW)
		res.actions = append(res.actions, naiveAction{restore: true, id: l.spec.ID})
	}
	n.decision = fmt.Sprintf("restore: P=%g bound=%g restored=%v stoppedAt=%d",
		power, bound, res.actions, stoppedAt)
	return res
}

// peak recomputes the record from scratch over every closed window.
func (n *naive) peak() (float64, int64, bool) {
	if len(n.reports) == 0 {
		return 0, 0, false
	}
	first := n.reports[0].t
	now := n.now()
	best := 0.0
	bestEnd := int64(0)
	ok := false
	for end := (first/n.cfg.SlipSeconds + 1) * n.cfg.SlipSeconds; end <= now; end += n.cfg.SlipSeconds {
		d := n.energyBetween(end-n.cfg.WindowSeconds, end) / float64(n.cfg.WindowSeconds)
		if !ok || d > best {
			best, bestEnd, ok = d, end, true
		}
	}
	return best, bestEnd, ok
}
