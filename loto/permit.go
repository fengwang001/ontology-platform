package loto

import (
	"fmt"
	"sort"
)

// Apply 申请工作票。
// devices 为设备集合；start/end 为左闭右开计划时段 [start,end)；
// workers 为登记作业人员（至少一人）。申请不带时钟（以计划时刻为准）。
func (s *System) Apply(permitID, applicant string, devices []string, workType WorkType, start, end int64, workers []string) error {
	// 1) 参数合法性最先判定。
	if permitID == "" {
		return fail(InvalidParam, "permit id is empty")
	}
	if start < 0 || end < 0 || end <= start {
		return fail(InvalidParam, "bad planned window [%d,%d): require 0<=start<end", start, end)
	}
	if workType != WorkNormal && workType != WorkHighRisk && workType != WorkObservation {
		return fail(InvalidParam, "unknown work type %q", workType)
	}
	if applicant == "" {
		return fail(InvalidParam, "applicant is empty")
	}
	devSet := map[string]bool{}
	for _, d := range devices {
		if d == "" {
			return fail(InvalidParam, "device id is empty")
		}
		devSet[d] = true
	}
	if len(devSet) == 0 {
		return fail(InvalidParam, "permit %q has no devices", permitID)
	}
	workerSet := map[string]bool{}
	for _, w := range workers {
		if w == "" {
			return fail(InvalidParam, "worker id is empty")
		}
		workerSet[w] = true
	}
	if len(workerSet) == 0 {
		return fail(InvalidParam, "permit %q must register at least one worker", permitID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.st.permits[permitID]; dup {
		return fail(InvalidParam, "permit %q already exists", permitID)
	}
	// 2) 对象存在性。
	app, ok := s.st.persons[applicant]
	if !ok {
		return fail(NotFound, "applicant %q not registered", applicant)
	}
	for d := range devSet {
		if _, ok := s.st.devices[d]; !ok {
			return fail(NotFound, "device %q not registered", d)
		}
	}
	for w := range workerSet {
		if _, ok := s.st.persons[w]; !ok {
			return fail(NotFound, "worker %q not registered", w)
		}
	}
	// 3) 角色。
	if !app.Roles[RoleApplicant] {
		return fail(PermissionDenied, "%q lacks role applicant", applicant)
	}
	for w := range workerSet {
		if !s.st.persons[w].Roles[RoleWorker] {
			return fail(PermissionDenied, "%q lacks role worker", w)
		}
	}

	// 隔离点并集在申请时快照（设备配置后续变化不影响本票）。
	points := map[string]bool{}
	for d := range devSet {
		for pt := range s.st.devices[d] {
			points[pt] = true
		}
	}

	p := &Permit{
		ID:        permitID,
		Applicant: applicant,
		Devices:   devSet,
		Points:    points,
		Workers:   workerSet,
		WorkType:  workType,
		Start:     start,
		End:       end,
		Phase:     PhasePending,
		Approvers: map[string]bool{},
		Inside:    map[string]bool{},
		Locks:     map[string]*Lock{},
	}
	s.st.permits[permitID] = p
	s.log(0, "apply", applicant, permitID,
		fmt.Sprintf("devices=%v type=%s window=[%d,%d) workers=%d points=%d",
			sortedKeys(devSet), workType, start, end, len(workerSet), len(points)))
	return nil
}

// intervalsOverlap 左闭右开区间相交（首尾相接不算）。
func intervalsOverlap(a0, a1, b0, b1 int64) bool { return a0 < b1 && b0 < a1 }

// conflictWith 检查本票与某占用票是否冲突（仅交集设备；双观察豁免）。
func (p *Permit) conflictWith(q *Permit) (string, bool) {
	if p.WorkType == WorkObservation && q.WorkType == WorkObservation {
		return "", false
	}
	for d := range p.Devices {
		if q.Devices[d] {
			if intervalsOverlap(p.Start, p.End, q.Start, q.End) {
				return d, true
			}
		}
	}
	return "", false
}

// Approve 批准工作票。
// 普通/只读观察票：任一非申请人批准人一人即生效。
// 高风险票：需要两个互不相同、且都不是申请人的批准人；第二次批准才生效。
// 冲突只在"使票生效的那次批准"判定；被拒绝的批准不消耗名额、不改时钟。
func (s *System) Approve(permitID, approver string, at int64) error {
	if permitID == "" || approver == "" {
		return fail(InvalidParam, "permit id and approver are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(at); err != nil {
		return err
	}
	p, ok := s.st.permits[permitID]
	if !ok {
		return fail(NotFound, "permit %q not found", permitID)
	}
	ap, ok := s.st.persons[approver]
	if !ok {
		return fail(NotFound, "approver %q not registered", approver)
	}
	if !ap.Roles[RoleApprover] {
		return fail(PermissionDenied, "%q lacks role approver", approver)
	}
	if p.Phase != PhasePending {
		return fail(StateNotAllowed, "permit %q is not pending (phase=%s)", permitID, p.Phase)
	}

	becomesEffective := p.WorkType != WorkHighRisk || len(p.Approvers)+1 >= 2
	if becomesEffective {
		// 冲突判定只遍历本票设备上的占用态票（activeByDevice 索引），与历史完成票总数无关。
		var conflicts []string
		for d := range p.Devices {
			for qid := range s.st.activeByDevice[d] {
				q := s.st.permits[qid]
				if dev, bad := p.conflictWith(q); bad {
					conflicts = append(conflicts, fmt.Sprintf("device=%s with permit=%s window=[%d,%d) type=%s",
						dev, qid, q.Start, q.End, q.WorkType))
				}
			}
		}
		if len(conflicts) > 0 {
			sort.Strings(conflicts)
			return fail(Conflict, "permit %q rejected: %v", permitID, conflicts)
		}
	}
	// 条件（批准人≠申请人、同一人不重复计数）排在冲突之后：被冲突拒绝的批准不消耗名额。
	if approver == p.Applicant {
		return fail(ConditionNotMet, "approver must differ from applicant %q", p.Applicant)
	}
	if p.Approvers[approver] {
		return fail(ConditionNotMet, "approver %q already approved (names not consumed on rejection)", approver)
	}

	s.advanceClock(at)
	p.Approvers[approver] = true
	if becomesEffective {
		p.Phase = PhaseEffective
		for d := range p.Devices {
			set := s.st.activeByDevice[d]
			if set == nil {
				set = map[string]bool{}
				s.st.activeByDevice[d] = set
			}
			set[permitID] = true
		}
		s.log(at, "approve_effective", approver, permitID,
			fmt.Sprintf("approvers=%d/%d", len(p.Approvers), requiredApprovals(p)))
	} else {
		s.log(at, "approve_partial", approver, permitID,
			fmt.Sprintf("high-risk approvers=%d/2 (not yet effective)", len(p.Approvers)))
	}
	return nil
}

func requiredApprovals(p *Permit) int {
	if p.WorkType == WorkHighRisk {
		return 2
	}
	return 1
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
