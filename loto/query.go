package loto

import "sort"

// EnergizeReport 是送电判定的判定依据（供日志打印）。
type EnergizeReport struct {
	Device      string
	Energizable bool
	Reasons     []string
}

// PermitSnapshot 工作票只读快照（测试与朴素模型对照用）。
type PermitSnapshot struct {
	ID            string
	Applicant     string
	Phase         Phase
	Overdue       bool
	Devices       []string
	Points        []string
	Workers       []string
	Approvers     []string
	Inside        []string
	PhysicalLocks []string // "worker@point" 形式的当前物理在位锁
	Start, End    int64
}

// GetPermit 返回票的只读快照；不存在返回 (nil,false)。
func (s *System) GetPermit(id string) (PermitSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.st.permits[id]
	if !ok {
		return PermitSnapshot{}, false
	}
	snap := PermitSnapshot{
		ID: p.ID, Applicant: p.Applicant, Phase: p.Phase, Overdue: p.Overdue,
		Devices: sortedKeys(p.Devices), Points: sortedKeys(p.Points),
		Workers: sortedKeys(p.Workers), Approvers: sortedKeys(p.Approvers),
		Inside: sortedKeys(p.Inside), Start: p.Start, End: p.End,
	}
	for _, lk := range p.Locks {
		if !lk.Removed && !lk.TrialRemoved {
			snap.PhysicalLocks = append(snap.PhysicalLocks, lk.Worker+"@"+lk.Point)
		}
	}
	sort.Strings(snap.PhysicalLocks)
	return snap, true
}

// TotalPermits 返回系统中累计票数（含历史完成票；供规模对照测试）。
func (s *System) TotalPermits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.st.permits)
}
