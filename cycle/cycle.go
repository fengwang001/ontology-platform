// Package cycle breaks rename cycles by borrowing one temporary name
// per cycle, so every step's target is free at execution time.
package cycle

import (
	"errors"
	"fmt"

	"ontology/name"
	"ontology/plan"
)

// ErrNoTempName is returned when no free temporary name can be found.
var ErrNoTempName = errors.New("cycle: no free temporary name")

const (
	prefix = "tmp~"
	maxTry = 1 << 20
)

// Break converts each cycle into a safe step sequence using exactly one
// temporary name per cycle. For cycle r0..r(k-1) with r(i+1).From == ri.To
// it emits: r0.From->t, r(k-1)..r1 in order, t->r0.To. Temporary names are
// checked against the namespace, all batch names and previously used temps.
func Break(ns *name.Set, reqs []plan.Request, cycles [][]plan.Request) (steps []plan.Step, temps []string, err error) {
	reserved := make(map[string]bool, 2*len(reqs))
	for _, r := range reqs {
		reserved[r.From] = true
		reserved[r.To] = true
	}
	next := 0
	for _, cyc := range cycles {
		t, terr := tempName(ns, reserved, &next)
		if terr != nil {
			return nil, nil, terr
		}
		reserved[t] = true
		temps = append(temps, t)
		steps = append(steps, plan.Step{From: cyc[0].From, To: t})
		for i := len(cyc) - 1; i >= 1; i-- {
			steps = append(steps, plan.Step{From: cyc[i].From, To: cyc[i].To})
		}
		steps = append(steps, plan.Step{From: t, To: cyc[0].To})
	}
	return steps, temps, nil
}

func tempName(ns *name.Set, reserved map[string]bool, next *int) (string, error) {
	for ; *next < maxTry; *next++ {
		t := fmt.Sprintf("%s%d", prefix, *next)
		if reserved[t] || ns.Contains(t) {
			continue
		}
		return t, nil
	}
	return "", ErrNoTempName
}
