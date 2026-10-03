package saga

import (
	"ontology/effect"
	"ontology/journal"
)

// PlanKind 是恢复计划的种类。
type PlanKind int

const (
	PlanForward     PlanKind = iota // 前滚：执行步骤 Step
	PlanCompensate                  // 补偿：撤销步骤 Step
	PlanProbe                       // 探测枢轴是否已提交
	PlanFinished                    // 已完成（全部步骤成功）
	PlanCompensated                 // 已补偿（回退到起点）
	PlanManual                      // 转人工（既不能前滚也不能回退）
)

// Plan 是 Recover 根据日志前缀得出的恢复计划。
type Plan struct {
	Kind PlanKind
	Step int // 仅 PlanForward 与 PlanCompensate 有意义
}

// planOf 只由日志记录与实例参数推导恢复计划，是纯函数。
// 规则见 DESIGN.md：只看最后一条记录，F 的瞬时计数扫描全日志。
func planOf(recs []journal.Record, n, p, budget int) Plan {
	last := recs[len(recs)-1]
	switch last.Kind {
	case journal.KindBegin:
		return Plan{Kind: PlanForward, Step: 0}
	case journal.KindDone:
		if last.Step == n-1 {
			return Plan{Kind: PlanFinished}
		}
		return Plan{Kind: PlanForward, Step: last.Step + 1}
	case journal.KindIntent:
		switch {
		case last.Step < p:
			// 结果未知且可补偿：直接补偿（含本步，可能已生效）。
			return Plan{Kind: PlanCompensate, Step: last.Step}
		case last.Step == p:
			return Plan{Kind: PlanProbe}
		default:
			// 枢轴后不可补偿：重做，副作用幂等。
			return Plan{Kind: PlanForward, Step: last.Step}
		}
	case journal.KindFail:
		return failPlan(recs, n, p, budget, last.Step)
	case journal.KindCompIntent:
		return Plan{Kind: PlanCompensate, Step: last.Step}
	case journal.KindCompDone:
		if last.Step == 0 {
			return Plan{Kind: PlanCompensated}
		}
		return Plan{Kind: PlanCompensate, Step: last.Step - 1}
	}
	panic("saga: 未知记录类型")
}

// failPlan 处理最后一条为 F(i,k) 的情形。
func failPlan(recs []journal.Record, n, p, budget, i int) Plan {
	last := recs[len(recs)-1]
	transient := 0
	for _, r := range recs {
		if r.Kind == journal.KindFail && r.Step == i && r.Fail == journal.Transient {
			transient++
		}
	}
	terminal := last.Fail == journal.Permanent || (i <= p && transient > budget)
	if !terminal {
		return Plan{Kind: PlanForward, Step: i}
	}
	if i > p {
		return Plan{Kind: PlanManual}
	}
	if i == 0 {
		return Plan{Kind: PlanCompensated}
	}
	return Plan{Kind: PlanCompensate, Step: i - 1}
}

// CompIntent 登记补偿意图：写 CI(j) 并登记 (id,j,comp)。要求计划为补偿 j。
func (m *Manager) CompIntent(id string, j int) error {
	ins, err := m.get(id)
	if err != nil {
		return err
	}
	defer ins.mu.Unlock()
	if err := ins.checkStep(j); err != nil {
		return err
	}
	if ins.inflight >= 0 {
		return ErrState
	}
	pl := m.plan(id, ins)
	if pl.Kind != PlanCompensate {
		return ErrState
	}
	if pl.Step != j {
		return ErrStep
	}
	m.jr.Append(id, journal.Record{Kind: journal.KindCompIntent, Step: j})
	m.ef.Register(effect.Key{ID: id, Step: j, Phase: effect.Comp})
	ins.inflight, ins.comp = j, true
	return nil
}

// CompDone 报告补偿 j 完成：写 CD(j)。要求同下标的 CI 在途。
func (m *Manager) CompDone(id string, j int) error {
	ins, err := m.get(id)
	if err != nil {
		return err
	}
	defer ins.mu.Unlock()
	if err := ins.checkStep(j); err != nil {
		return err
	}
	if ins.inflight < 0 || !ins.comp {
		return ErrState
	}
	if ins.inflight != j {
		return ErrStep
	}
	m.jr.Append(id, journal.Record{Kind: journal.KindCompDone, Step: j})
	ins.inflight = -1
	return nil
}

// Probe 探测枢轴：committed 为 true 写 D(p)，false 写 F(p,永久)。
// 仅在计划为“探测”时合法。
func (m *Manager) Probe(id string, committed bool) error {
	ins, err := m.get(id)
	if err != nil {
		return err
	}
	defer ins.mu.Unlock()
	if ins.inflight >= 0 {
		return ErrState
	}
	pl := m.plan(id, ins)
	if pl.Kind != PlanProbe {
		return ErrState
	}
	rec := journal.Record{Kind: journal.KindDone, Step: ins.p}
	if !committed {
		rec = journal.Record{Kind: journal.KindFail, Step: ins.p, Fail: journal.Permanent}
	}
	m.jr.Append(id, rec)
	ins.inflight = -1
	return nil
}

// Recover 只凭日志推导恢复计划：不写日志、不登记副作用，
// 放弃在途（清除内存标志）。只读且幂等，
// 单次读取记录数等于该实例日志条数。
func (m *Manager) Recover(id string) (Plan, error) {
	ins, err := m.get(id)
	if err != nil {
		return Plan{}, err
	}
	defer ins.mu.Unlock()
	recs := m.jr.Records(id)
	m.lastReads.Store(int64(len(recs)))
	pl := planOf(recs, ins.n, ins.p, ins.budget)
	ins.inflight = -1
	return pl, nil
}
