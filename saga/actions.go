package saga

import (
	"fmt"

	"ontology/effect"
	"ontology/journal"
)

// Intent 写 I(i) 并登记 (id,i,fwd)；要求无在途步骤且计划为前滚 i。
func (s *Saga) Intent(id []byte, i int) error {
	if err := checkStepGlobal(i); err != nil {
		return err
	}
	key, inst, err := s.lookup(id)
	if err != nil {
		return err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if err := checkStep(inst, i); err != nil {
		return err
	}
	if inst.inFlight {
		return fmt.Errorf("%w: 步骤 %d 在途，不能再起意图", ErrState, inst.flightStep)
	}
	plan := planOf(s.jr.Read(key), inst.n, inst.p, inst.budget)
	if plan.Kind != Forward {
		return fmt.Errorf("%w: 计划为 %v，不能前滚", ErrState, plan)
	}
	if plan.Step != i {
		return fmt.Errorf("%w: 计划前滚 %d，收到 %d", ErrStep, plan.Step, i)
	}
	s.jr.Append(key, journal.Record{Kind: journal.KindIntent, Step: i})
	s.eff.Register(key, i, effect.Fwd) // 先写日志、后登记副作用
	inst.inFlight, inst.flightStep, inst.flightComp = true, i, false
	return nil
}

// CompIntent 写 CI(j) 并登记 (id,j,comp)；要求无在途步骤且计划为补偿 j。
func (s *Saga) CompIntent(id []byte, j int) error {
	if err := checkStepGlobal(j); err != nil {
		return err
	}
	key, inst, err := s.lookup(id)
	if err != nil {
		return err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if err := checkStep(inst, j); err != nil {
		return err
	}
	if inst.inFlight {
		return fmt.Errorf("%w: 步骤 %d 在途，不能再起补偿", ErrState, inst.flightStep)
	}
	plan := planOf(s.jr.Read(key), inst.n, inst.p, inst.budget)
	if plan.Kind != Compensate {
		return fmt.Errorf("%w: 计划为 %v，不能补偿", ErrState, plan)
	}
	if plan.Step != j {
		return fmt.Errorf("%w: 计划补偿 %d，收到 %d", ErrStep, plan.Step, j)
	}
	s.jr.Append(key, journal.Record{Kind: journal.KindCompIntent, Step: j})
	s.eff.Register(key, j, effect.Comp)
	inst.inFlight, inst.flightStep, inst.flightComp = true, j, true
	return nil
}

// pair 处理与在途步骤配对的收尾动作：Done/CompDone/Fail 共用。
func (s *Saga) pair(id []byte, i int, comp bool, rec journal.Record) error {
	if err := checkStepGlobal(i); err != nil {
		return err
	}
	key, inst, err := s.lookup(id)
	if err != nil {
		return err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if err := checkStep(inst, i); err != nil {
		return err
	}
	if !inst.inFlight || inst.flightComp != comp || inst.flightStep != i {
		return fmt.Errorf("%w: 步骤 %d 无配对的在途意图", ErrState, i)
	}
	s.jr.Append(key, rec)
	inst.inFlight = false
	return nil
}

// Done 写 D(i)；要求同下标的 I 在途。
func (s *Saga) Done(id []byte, i int) error {
	return s.pair(id, i, false, journal.Record{Kind: journal.KindDone, Step: i})
}

// Fail 写 F(i,k)；要求同下标的 I 在途，k 为瞬时或永久。
func (s *Saga) Fail(id []byte, i int, k journal.FailKind) error {
	if k != journal.Transient && k != journal.Permanent {
		return fmt.Errorf("%w: 失败类别 %d", ErrInvalidArg, k)
	}
	return s.pair(id, i, false, journal.Record{Kind: journal.KindFail, Step: i, Fail: k})
}

// CompDone 写 CD(j)；要求同下标的 CI 在途。
func (s *Saga) CompDone(id []byte, j int) error {
	return s.pair(id, j, true, journal.Record{Kind: journal.KindCompDone, Step: j})
}

// Probe 仅在计划为探测且无在途步骤时合法：true 写 D(p)，false 写 F(p,永久)。
func (s *Saga) Probe(id []byte, committed bool) error {
	key, inst, err := s.lookup(id)
	if err != nil {
		return err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.inFlight {
		return fmt.Errorf("%w: 步骤 %d 在途，不能探测", ErrState, inst.flightStep)
	}
	plan := planOf(s.jr.Read(key), inst.n, inst.p, inst.budget)
	if plan.Kind != ProbePivot {
		return fmt.Errorf("%w: 计划为 %v，不能探测", ErrState, plan)
	}
	rec := journal.Record{Kind: journal.KindDone, Step: inst.p}
	if !committed {
		rec = journal.Record{Kind: journal.KindFail, Step: inst.p, Fail: journal.Permanent}
	}
	s.jr.Append(key, rec)
	return nil
}

// Recover 只由日志前缀推出计划；不写日志，放弃在途，幂等。
func (s *Saga) Recover(id []byte) (Plan, error) {
	key, inst, err := s.lookup(id)
	if err != nil {
		return Plan{}, err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.inFlight = false // 在途被放弃：其后同一步的迟到 Done 报状态不符
	return planOf(s.jr.Read(key), inst.n, inst.p, inst.budget), nil
}
