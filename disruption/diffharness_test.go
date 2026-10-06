package disruption

import (
	"math/rand"
)

// diffWorld is the bounded random universe shared by both implementations.
type diffWorld struct {
	rng        *rand.Rand
	namespaces []string
	podNames   []string
	podKeys    []string // label keys
	budNames   []string
	tick       Tick
}

func newDiffWorld(seed int64) *diffWorld {
	return &diffWorld{
		rng:        rand.New(rand.NewSource(seed)),
		namespaces: []string{"ns0", "ns1"},
		podNames:   []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6"},
		podKeys:    []string{"app", "tier"},
		budNames:   []string{"b0", "b1"},
	}
}

func (w *diffWorld) id() PodID {
	return PodID{Namespace: w.namespaces[w.rng.Intn(len(w.namespaces))], Name: w.podNames[w.rng.Intn(len(w.podNames))]}
}

func (w *diffWorld) budgetID() BudgetID {
	return BudgetID{Namespace: w.namespaces[w.rng.Intn(len(w.namespaces))], Name: w.budNames[w.rng.Intn(len(w.budNames))]}
}

func (w *diffWorld) labels() map[string]string {
	out := map[string]string{}
	for _, k := range w.podKeys {
		switch w.rng.Intn(3) {
		case 0:
			out[k] = "a"
		case 1:
			out[k] = "b"
		}
	}
	return out
}

func (w *diffWorld) budget() PodDisruptionBudget {
	sel := Selector{}
	// 1/6 chance of empty selector (matches nothing).
	if w.rng.Intn(6) != 0 {
		k := w.podKeys[w.rng.Intn(len(w.podKeys))]
		sel[k] = []string{"a", "b"}[w.rng.Intn(2)]
		if w.rng.Intn(3) == 0 {
			k2 := w.podKeys[(indexOf(k, w.podKeys)+1)%len(w.podKeys)]
			sel[k2] = []string{"a", "b"}[w.rng.Intn(2)]
		}
	}
	var v *IntOrPct
	if w.rng.Intn(2) == 0 {
		if w.rng.Intn(2) == 0 {
			v = pct(w.rng.Intn(101))
		} else {
			v = abs(w.rng.Intn(5))
		}
		b := PodDisruptionBudget{ID: w.budgetID(), Selector: sel, MinAvailable: v}
		return b
	}
	if w.rng.Intn(2) == 0 {
		v = pct(w.rng.Intn(101))
	} else {
		v = abs(w.rng.Intn(5))
	}
	return PodDisruptionBudget{ID: w.budgetID(), Selector: sel, MaxUnavail: v}
}

func indexOf(s string, xs []string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

func (w *diffWorld) phase() Phase {
	return []Phase{PhaseRunning, PhaseRunning, PhaseRunning, PhasePending, PhaseSucceeded, PhaseFailed}[w.rng.Intn(6)]
}

// adv advances the logical clock by a small non-negative delta; occasionally
// it jumps to exactly a grace boundary.
func (w *diffWorld) adv() Tick {
	w.tick += Tick(w.rng.Intn(3))
	return w.tick
}

func (w *diffWorld) grace() Tick {
	g := []Tick{0, 1, 2, 3, 10}[w.rng.Intn(5)]
	return g
}

type snapshot struct {
	quota map[BudgetID]BudgetStatus
	now   Tick
}
