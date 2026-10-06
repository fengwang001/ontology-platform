package hd

// 可行性判定。所有判定只做常数次索引邻近查询，不扫描任何机位/患者的历史。

// slotView 抽象一组"已存在"的治疗：正式运行时只含已提交数据；
// 周期方案展开时叠加尚未提交的虚拟分配。
type slotView interface {
	bayNeighbors(bayID string, start int) (pred, succ *Treatment)
	bayAt(bayID string, start int) *Treatment
	patientNeighbors(patientID string, start int) (pred, succ *Treatment)
	patientAt(patientID string, start int) *Treatment
}

// liveView 直接读取已提交索引。
type liveView struct{ s *System }

func (v liveView) bayNeighbors(bayID string, start int) (*Treatment, *Treatment) {
	return v.s.byBay[bayID].committed.neighbors(start)
}

func (v liveView) bayAt(bayID string, start int) *Treatment {
	return v.s.byBay[bayID].committed.at(start)
}

func (v liveView) patientNeighbors(patientID string, start int) (*Treatment, *Treatment) {
	return v.s.byPatient[patientID].committed.neighbors(start)
}

func (v liveView) patientAt(patientID string, start int) *Treatment {
	return v.s.byPatient[patientID].committed.at(start)
}

func (tr *treap) at(key int) *Treatment {
	cur := tr.root
	for cur != nil {
		switch {
		case key < cur.t.Start:
			cur = cur.left
		case key > cur.t.Start:
			cur = cur.right
		default:
			return cur.t
		}
	}
	return nil
}

// bayZoneEligible 判断机位区域/观察位属性是否允许该感染状态使用。
func (s *System) bayZoneEligible(bay *Bay, inf Infection) bool {
	switch inf {
	case InfectionHBV, InfectionHCV:
		return bay.Zone == ZoneIsolation
	case InfectionNegative:
		return bay.Zone == ZoneGeneral
	case InfectionPending:
		return bay.Zone == ZoneGeneral && bay.Observed
	default:
		return false
	}
}

// bayFaultFree 判断治疗区间是否与该机位任何故障停用区间相交。
// 区间相交（含消毒不涉及故障判定，故障只影响治疗本身的承担）。
func (s *System) bayFaultFree(bayID string, start, end int) bool {
	f := s.faults[bayID]
	if f == nil {
		return true
	}
	recoverAt := f.RecoverAt
	if !f.Recovered {
		recoverAt = maxTime + 1
	}
	return !(start < recoverAt && end > f.Start)
}

// requiredGap 给出同一机位上 prev 治疗与下一次治疗(inf)之间所需的最小间隔：
// 从 prev.End 到下一次 Start。感染类型不同（仅可能在隔离区）用深度消毒，
// 否则用 prev 患者状态对应的常规消毒。
func (s *System) requiredGap(prev *Treatment, nextInf Infection) int {
	if prev.Infection != nextInf {
		return s.cfg.DeepClean
	}
	return s.cfg.cleanDuration(prev.Infection)
}

// bayFeasible 判断某次治疗能否放入指定机位（不含患者自身冲突）。
func (s *System) bayFeasible(v slotView, bay *Bay, patientID string, inf Infection, start, end int) bool {
	if !s.bayZoneEligible(bay, inf) {
		return false
	}
	// 登记为不可用且从未发生故障的机位不能承担治疗；故障中的机位由区间判定处理。
	if !bay.Available {
		if _, faulted := s.faults[bay.ID]; !faulted {
			return false
		}
	}
	if !s.bayFaultFree(bay.ID, start, end) {
		return false
	}
	if existing := v.bayAt(bay.ID, start); existing != nil {
		return false
	}
	pred, succ := v.bayNeighbors(bay.ID, start)
	if pred != nil {
		if start < pred.End+s.requiredGap(pred, inf) {
			return false
		}
	}
	if succ != nil {
		if succ.Start < end+s.requiredGap(&Treatment{Infection: inf}, succ.Infection) {
			return false
		}
	}
	return true
}

// patientConflict 判断同一患者的恢复间隔/重叠约束是否被破坏。
func (s *System) patientConflict(v slotView, patientID string, start, end int) bool {
	if t := v.patientAt(patientID, start); t != nil {
		return true
	}
	pred, succ := v.patientNeighbors(patientID, start)
	if pred != nil && start < pred.End+s.cfg.MinRecovery {
		return true
	}
	if succ != nil && succ.Start < end+s.cfg.MinRecovery {
		return true
	}
	return false
}

// findBay 按编号升序返回第一个可行机位；zoneEligibleAny 报告是否至少存在
// 区域属性合格的机位（用于区分感染隔离冲突与无可行机位）。
func (s *System) findBay(v slotView, patientID string, inf Infection, start, end int, allowed map[string]bool) (string, bool, bool) {
	zoneEligibleAny := false
	for _, id := range s.bayOrder {
		bay := s.bays[id]
		if allowed != nil && !allowed[id] {
			continue
		}
		if !s.bayZoneEligible(bay, inf) {
			continue
		}
		zoneEligibleAny = true
		if s.bayFeasible(v, bay, patientID, inf, start, end) {
			return id, true, true
		}
	}
	return "", zoneEligibleAny, false
}
