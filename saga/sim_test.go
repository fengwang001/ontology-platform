package saga_test

import (
	"ontology/journal"
	"ontology/saga"
)

// act 为可重放的动作编码。
type act struct {
	op   string // "I","D","Ft","Fp","CI","CD","Pt","Pf"
	step int
}

var kindOf = map[string]journal.Kind{
	"I": journal.KindIntent, "D": journal.KindDone, "Ft": journal.KindFail, "Fp": journal.KindFail,
	"CI": journal.KindCompIntent, "CD": journal.KindCompDone, "Pt": journal.KindDone, "Pf": journal.KindFail,
}

func (a act) record() journal.Record {
	r := journal.Record{Kind: kindOf[a.op], Step: a.step}
	if a.op == "Ft" {
		r.Fail = journal.Transient
	} else if a.op == "Fp" || a.op == "Pf" {
		r.Fail = journal.Permanent
	}
	return r
}

func applyAct(s *saga.Saga, id []byte, a act) error {
	switch a.op {
	case "I":
		return s.Intent(id, a.step)
	case "D":
		return s.Done(id, a.step)
	case "Ft":
		return s.Fail(id, a.step, journal.Transient)
	case "Fp":
		return s.Fail(id, a.step, journal.Permanent)
	case "CI":
		return s.CompIntent(id, a.step)
	case "CD":
		return s.CompDone(id, a.step)
	case "Pt":
		return s.Probe(id, true)
	case "Pf":
		return s.Probe(id, false)
	}
	panic("bad act")
}

// naivePlan 为按规格逐步写成的朴素模拟：先扫全日志统计瞬时失败数，再按最后一条记录判定。
func naivePlan(recs []journal.Record, n, p, budget int) saga.Plan {
	last := recs[len(recs)-1]
	switch last.Kind {
	case journal.KindBegin:
		return saga.Plan{Kind: saga.Forward, Step: 0}
	case journal.KindDone:
		if last.Step == n-1 {
			return saga.Plan{Kind: saga.Completed, Step: -1}
		}
		return saga.Plan{Kind: saga.Forward, Step: last.Step + 1}
	case journal.KindIntent:
		if last.Step < p {
			return saga.Plan{Kind: saga.Compensate, Step: last.Step}
		}
		if last.Step == p {
			return saga.Plan{Kind: saga.ProbePivot, Step: -1}
		}
		return saga.Plan{Kind: saga.Forward, Step: last.Step}
	case journal.KindFail:
		c := 0
		for _, r := range recs {
			if r.Kind == journal.KindFail && r.Step == last.Step && r.Fail == journal.Transient {
				c++
			}
		}
		if last.Fail != journal.Permanent && (last.Step > p || c <= budget) {
			return saga.Plan{Kind: saga.Forward, Step: last.Step}
		}
		if last.Step > p {
			return saga.Plan{Kind: saga.Manual, Step: -1}
		}
		if last.Step == 0 {
			return saga.Plan{Kind: saga.Compensated, Step: -1}
		}
		return saga.Plan{Kind: saga.Compensate, Step: last.Step - 1}
	case journal.KindCompIntent:
		return saga.Plan{Kind: saga.Compensate, Step: last.Step}
	case journal.KindCompDone:
		if last.Step == 0 {
			return saga.Plan{Kind: saga.Compensated, Step: -1}
		}
		return saga.Plan{Kind: saga.Compensate, Step: last.Step - 1}
	}
	panic("bad record")
}

// nextActs 由日志前缀推出全部合法下一步动作。
func nextActs(recs []journal.Record, n, p, budget int) []act {
	last := recs[len(recs)-1]
	switch last.Kind {
	case journal.KindIntent:
		return []act{{"D", last.Step}, {"Ft", last.Step}, {"Fp", last.Step}}
	case journal.KindCompIntent:
		return []act{{"CD", last.Step}}
	}
	switch pl := naivePlan(recs, n, p, budget); pl.Kind {
	case saga.Forward:
		return []act{{"I", pl.Step}}
	case saga.Compensate:
		return []act{{"CI", pl.Step}}
	case saga.ProbePivot:
		return []act{{"Pt", p}, {"Pf", p}}
	}
	return nil // 终态
}
