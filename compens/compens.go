package compens

import "ontology/step"

// Plan describes one compensation pass. Order lists step indices that must be
// compensated now, strictly descending; Skip lists succeeded indices that
// already have a durable compensation-success record (idempotent replay must
// not invoke them again).
type Plan struct {
	Order []int
	Skip  []int
}

// Build derives the plan purely from the durable sets:
//   - succeeded[i]: a forward effect may exist (FwdSuccess or FwdUnknown);
//   - alreadyCompensated[i]: a CompensationSuccess record exists.
//
// A step present in both sets is skipped. The resulting order is a strict
// prefix-free descending scan, so failed/never-run steps never appear.
func Build(steps []step.Step, succeeded []bool, alreadyCompensated []bool) Plan {
	p := Plan{}
	for i := len(steps) - 1; i >= 0; i-- {
		if i >= len(succeeded) || !succeeded[i] {
			continue
		}
		if i < len(alreadyCompensated) && alreadyCompensated[i] {
			p.Skip = append(p.Skip, i)
			continue
		}
		p.Order = append(p.Order, i)
	}
	return p
}
