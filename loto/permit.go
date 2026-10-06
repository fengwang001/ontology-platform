package loto

import (
	"fmt"
	"sort"
	"strings"
)

// 本文件实现工作票生命周期的全部带时刻操作。
// 每个操作严格按以下顺序校验，返回首个命中的错误：
//  1. 参数非法  2. 时刻回退  3. 对象不存在  4. 无权限或角色不符
//  5. 状态不允许  6. 冲突  7. 条件不满足
// 校验全部通过后才修改状态、推进时钟并写审计；被拒绝的操作不产生任何副作用。

func validWorkType(wt WorkType) bool {
	return wt == WorkNormal || wt == WorkHighRisk || wt == WorkReadOnly
}

func (s *System) getPermit(op string, id int) (*Permit, error) {
	p, ok := s.permits[id]
	if !ok {
		return nil, newErr(op, ErrNotFound, "工作票 %d 不存在", id)
	}
	return p, nil
}

func (s *System) getPerson(op, id string) (map[Role]bool, error) {
	r, ok := s.people[id]
	if !ok {
		return nil, newErr(op, ErrNotFound, "人员 %s 不存在", id)
	}
	return r, nil
}

func (s *System) checkPointExists(op, point string) error {
	if !s.points[point] {
		return newErr(op, ErrNotFound, "隔离点 %s 不存在", point)
	}
	return nil
}

// Apply 申请工作票。返回新票 ID。
func (s *System) Apply(t int64, applicant string, devices []string, wt WorkType, start, end int64) (int, error) {
	const op = "Apply"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if applicant == "" {
		return 0, newErr(op, ErrInvalidParam, "申请人不能为空")
	}
	if len(devices) == 0 {
		return 0, newErr(op, ErrInvalidParam, "设备集合不能为空")
	}
	seen := map[string]bool{}
	for _, d := range devices {
		if d == "" {
			return 0, newErr(op, ErrInvalidParam, "设备编号不能为空")
		}
		if seen[d] {
			return 0, newErr(op, ErrInvalidParam, "设备 %s 重复", d)
		}
		seen[d] = true
	}
	if !validWorkType(wt) {
		return 0, newErr(op, ErrInvalidParam, "非法作业类型 %d", int(wt))
	}
	if start < 0 || end <= start {
		return 0, newErr(op, ErrInvalidParam, "计划时段非法: [%d, %d)", start, end)
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return 0, err
	}
	// 3. 对象不存在
	if _, err := s.getPerson(op, applicant); err != nil {
		return 0, err
	}
	for _, d := range devices {
		if _, ok := s.cfg.DevicePoints[d]; !ok {
			return 0, newErr(op, ErrNotFound, "设备 %s 不存在", d)
		}
	}
	// 4. 无权限或角色不符
	if !s.people[applicant][RoleApplicant] {
		return 0, newErr(op, ErrPermission, "人员 %s 不具备申请人角色", applicant)
	}

	// 接受
	devs := append([]string(nil), devices...)
	sort.Strings(devs)
	pointSet := map[string]bool{}
	for _, d := range devs {
		for _, pt := range s.cfg.DevicePoints[d] {
			pointSet[pt] = true
		}
	}
	pts := make([]string, 0, len(pointSet))
	for pt := range pointSet {
		pts = append(pts, pt)
	}
	sort.Strings(pts)
	p := &Permit{
		ID:         s.nextPermitID,
		Applicant:  applicant,
		Devices:    devs,
		Type:       wt,
		Start:      start,
		End:        end,
		State:      StateApplied,
		approvers:  map[string]bool{},
		workers:    map[string]bool{},
		points:     pts,
		pointSet:   pointSet,
		locks:      map[string]map[string]bool{},
		onSite:     map[string]bool{},
		mustRelock: map[string]bool{},
	}
	s.permits[p.ID] = p
	s.nextPermitID++
	s.accept(t, op, p.ID, applicant, fmt.Sprintf("设备=%s 类型=%s 时段=[%d,%d)", strings.Join(devs, ","), wt, start, end))
	return p.ID, nil
}

// approvalsNeeded 返回该票生效所需批准人数。
func (p *Permit) approvalsNeeded() int {
	if p.Type == WorkHighRisk {
		return 2
	}
	return 1
}

// Approve 批准工作票。高风险票须两个互不相同的批准人，其余一人。
// 冲突在使票生效的那次批准时判定；被拒绝的批准不消耗批准名额。
func (s *System) Approve(t int64, approver string, permitID int) error {
	const op = "Approve"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if approver == "" || permitID <= 0 {
		return newErr(op, ErrInvalidParam, "批准人或票号非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, approver); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if !s.people[approver][RoleApprover] {
		return newErr(op, ErrPermission, "人员 %s 不具备批准人角色", approver)
	}
	if approver == p.Applicant {
		return newErr(op, ErrPermission, "批准人不得是申请人 %s", approver)
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateApplied {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可批准", p.ID, p.stateAt(t))
	}
	// 6. 冲突（仅当本次批准会使票生效时判定）
	if p.approvers[approver] {
		// 7. 条件不满足：同一批准人重复批准
		return newErr(op, ErrPrecondition, "批准人 %s 已批准过工作票 %d", approver, p.ID)
	}
	wouldTakeEffect := len(p.approvers)+1 >= p.approvalsNeeded()
	if wouldTakeEffect {
		if c := s.findConflict(p); c != nil {
			return newErr(op, ErrConflict, "与工作票 %d 时段与设备均相交（非双方只读观察）", c.ID)
		}
	}

	// 接受
	p.approvers[approver] = true
	if wouldTakeEffect {
		s.setState(p, StateEffective)
		s.refreshLockState(p) // 无登记作业人员时直接进入已上锁
	}
	s.accept(t, op, p.ID, approver, fmt.Sprintf("批准人=%s 第%d/%d次批准", approver, len(p.approvers), p.approvalsNeeded()))
	return nil
}

// findConflict 在占用态票中查找与 p 冲突者：时段相交且设备集合相交，
// 除非双方都是只读观察。候选仅为当前占用态票，与历史票总数无关。
func (s *System) findConflict(p *Permit) *Permit {
	for _, q := range s.active {
		if q.ID == p.ID {
			continue
		}
		if p.Type == WorkReadOnly && q.Type == WorkReadOnly {
			continue
		}
		if p.Start >= q.End || q.Start >= p.End {
			continue // 左闭右开时段不相交（首尾相接不算相交）
		}
		if devicesIntersect(p.Devices, q.Devices) {
			return q
		}
	}
	return nil
}

// devicesIntersect 判断两个已排序设备集合是否相交。
func devicesIntersect(a, b []string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			return true
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return false
}

// AddWorker 登记作业人员到票上。操作人须为申请人或主管。
func (s *System) AddWorker(t int64, actor string, permitID int, worker string) error {
	const op = "AddWorker"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if actor == "" || worker == "" || permitID <= 0 {
		return newErr(op, ErrInvalidParam, "操作人、作业人员或票号非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, actor); err != nil {
		return err
	}
	if _, err := s.getPerson(op, worker); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if actor != p.Applicant && !s.people[actor][RoleSupervisor] {
		return newErr(op, ErrPermission, "操作人 %s 不是该票申请人也不是主管", actor)
	}
	if !s.people[worker][RoleWorker] {
		return newErr(op, ErrPermission, "人员 %s 不具备作业人员角色", worker)
	}
	// 5. 状态不允许（验证后不可再登记，否则“全员上锁”失效）
	switch p.stateAt(t) {
	case StateApplied, StateEffective, StateLocked:
	default:
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可登记作业人员", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if p.workers[worker] {
		return newErr(op, ErrPrecondition, "作业人员 %s 已登记在工作票 %d 上", worker, p.ID)
	}

	// 接受
	p.workers[worker] = true
	s.refreshLockState(p)
	s.accept(t, op, p.ID, actor, "登记作业人员="+worker)
	return nil
}

// Lock 作业人员在隔离点上挂自己的锁。
// 同一人在同一隔离点上对同一张票只能有一把锁。
// 试运行结束后的恢复阶段，仅允许原持锁人重新挂上被暂时解除的锁。
func (s *System) Lock(t int64, permitID int, person, point string) error {
	const op = "Lock"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || person == "" || point == "" {
		return newErr(op, ErrInvalidParam, "票号、人员或隔离点非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, person); err != nil {
		return err
	}
	if err := s.checkPointExists(op, point); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if !p.workers[person] {
		return newErr(op, ErrPermission, "人员 %s 不是工作票 %d 的登记作业人员", person, p.ID)
	}
	// 5. 状态不允许
	switch p.stateAt(t) {
	case StateEffective, StateLocked, StateTrialRestore, StateOverdue:
	default:
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可上锁", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if !p.pointSet[point] {
		return newErr(op, ErrPrecondition, "隔离点 %s 不属于工作票 %d 的隔离点集", point, p.ID)
	}
	if p.locks[point][person] {
		return newErr(op, ErrPrecondition, "人员 %s 已在隔离点 %s 上对本票挂锁", person, point)
	}
	if p.State == StateTrialRestore && !p.trialSaved[point][person] {
		return newErr(op, ErrPrecondition, "人员 %s 不是隔离点 %s 上被暂时解除锁的原持锁人", person, point)
	}

	// 接受
	s.addLock(p, person, point)
	if p.mustRelock[person] && p.personLocksComplete(person) {
		delete(p.mustRelock, person) // 被强制摘除者已重新完成全部上锁
	}
	if p.State == StateTrialRestore {
		delete(p.trialSaved[point], person)
		if len(p.trialSaved[point]) == 0 {
			delete(p.trialSaved, point)
		}
		if len(p.trialSaved) == 0 {
			// 全部恢复：回到已上锁，须再次零能量验证后才可重新开工
			s.setState(p, StateLocked)
		}
	} else {
		s.refreshLockState(p)
	}
	s.accept(t, op, p.ID, person, "上锁@"+point)
	return nil
}

// Verify 零能量验证。验证人不得是该票的任何作业人员，且须具备批准人或主管资格。
func (s *System) Verify(t int64, permitID int, verifier string) error {
	const op = "Verify"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || verifier == "" {
		return newErr(op, ErrInvalidParam, "票号或验证人非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, verifier); err != nil {
		return err
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateLocked {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可验证", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足（验证人身份）
	if p.workers[verifier] {
		return newErr(op, ErrPrecondition, "验证人 %s 是该票作业人员，不得执行零能量验证", verifier)
	}
	if !s.people[verifier][RoleApprover] && !s.people[verifier][RoleSupervisor] {
		return newErr(op, ErrPrecondition, "验证人 %s 不具备验证资格（须为批准人或主管）", verifier)
	}

	// 接受
	s.setState(p, StateVerified)
	s.accept(t, op, p.ID, verifier, "零能量验证通过")
	return nil
}

// Start 开工。开工时刻须落在计划时段内（含起点，不含终点）。
func (s *System) Start(t int64, permitID int, actor string) error {
	const op = "Start"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || actor == "" {
		return newErr(op, ErrInvalidParam, "票号或操作人非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, actor); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if actor != p.Applicant && !s.people[actor][RoleSupervisor] {
		return newErr(op, ErrPermission, "操作人 %s 不是该票申请人也不是主管", actor)
	}
	// 5. 状态不允许（到达终点未开工的票已逾期）
	if p.stateAt(t) != StateVerified {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可开工", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if t < p.Start {
		return newErr(op, ErrPrecondition, "开工时刻 %d 早于计划起点 %d", t, p.Start)
	}
	if t >= p.End {
		return newErr(op, ErrPrecondition, "开工时刻 %d 不在计划时段 [%d,%d) 内", t, p.Start, p.End)
	}

	// 接受
	s.setState(p, StateStarted)
	s.accept(t, op, p.ID, actor, "开工")
	return nil
}

// personLocksComplete 报告某人是否已对本票全部隔离点上锁。
func (p *Permit) personLocksComplete(person string) bool {
	for _, pt := range p.points {
		if !p.locks[pt][person] {
			return false
		}
	}
	return true
}

// Enter 作业人员进入现场。须完成自己的全部上锁且票已开工；
// 被强制摘除锁的人须重新上锁后方可再次进入。
func (s *System) Enter(t int64, permitID int, person string) error {
	const op = "Enter"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || person == "" {
		return newErr(op, ErrInvalidParam, "票号或人员非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, person); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if !p.workers[person] {
		return newErr(op, ErrPermission, "人员 %s 不是工作票 %d 的登记作业人员", person, p.ID)
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateStarted {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可进入现场", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if p.onSite[person] {
		return newErr(op, ErrPrecondition, "人员 %s 已在现场", person)
	}
	if !p.personLocksComplete(person) {
		return newErr(op, ErrPrecondition, "人员 %s 未完成自己的全部上锁，不可进入现场", person)
	}
	if p.mustRelock[person] {
		return newErr(op, ErrPrecondition, "人员 %s 的锁曾被强制摘除，须重新上锁后方可进入", person)
	}

	// 接受
	p.onSite[person] = true
	s.accept(t, op, p.ID, person, "进入现场")
	return nil
}

// Leave 作业人员离开现场，离场后可再次进入。
func (s *System) Leave(t int64, permitID int, person string) error {
	const op = "Leave"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || person == "" {
		return newErr(op, ErrInvalidParam, "票号或人员非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, person); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if !p.workers[person] {
		return newErr(op, ErrPermission, "人员 %s 不是工作票 %d 的登记作业人员", person, p.ID)
	}
	// 5. 状态不允许（逾期票仍须允许人员离场）
	if st := p.stateAt(t); st != StateStarted && st != StateOverdue {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可离场", p.ID, st)
	}
	// 7. 条件不满足
	if !p.onSite[person] {
		return newErr(op, ErrPrecondition, "人员 %s 不在现场", person)
	}

	// 接受
	delete(p.onSite, person)
	s.accept(t, op, p.ID, person, "离开现场")
	return nil
}

// Complete 完工。须所有人均已离场。逾期票在全员离场后也可完工收尾。
func (s *System) Complete(t int64, permitID int, actor string) error {
	const op = "Complete"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || actor == "" {
		return newErr(op, ErrInvalidParam, "票号或操作人非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, actor); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if actor != p.Applicant && !s.people[actor][RoleSupervisor] {
		return newErr(op, ErrPermission, "操作人 %s 不是该票申请人也不是主管", actor)
	}
	// 5. 状态不允许
	if st := p.stateAt(t); st != StateStarted && st != StateOverdue {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可完工", p.ID, st)
	}
	// 7. 条件不满足
	if len(p.onSite) > 0 {
		return newErr(op, ErrPrecondition, "仍有 %d 人在现场，不可完工", len(p.onSite))
	}

	// 接受
	s.setState(p, StateCompleted)
	s.accept(t, op, p.ID, actor, "完工")
	return nil
}

// Unlock 完工后摘除自己的锁。只能由持锁人本人执行。
func (s *System) Unlock(t int64, permitID int, person, point string) error {
	const op = "Unlock"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || person == "" || point == "" {
		return newErr(op, ErrInvalidParam, "票号、人员或隔离点非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, person); err != nil {
		return err
	}
	if err := s.checkPointExists(op, point); err != nil {
		return err
	}
	// 4. 无权限或角色不符：该隔离点上本票有锁但非本人持有
	if len(p.locks[point]) > 0 && !p.locks[point][person] {
		return newErr(op, ErrPermission, "隔离点 %s 上本票的锁由他人持有，摘锁只能由持锁人本人执行", point)
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateCompleted {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，完工后才允许摘锁", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if !p.locks[point][person] {
		return newErr(op, ErrPrecondition, "人员 %s 在隔离点 %s 上对本票没有锁", person, point)
	}

	// 接受
	s.removeLock(p, person, point)
	s.accept(t, op, p.ID, person, "摘锁@"+point)
	return nil
}

// TrialBegin 申请试运行。须持票人（申请人）发起、票已开工且全员离场。
// 所有作业人员的锁暂时解除并保留记录。
func (s *System) TrialBegin(t int64, permitID int, actor string) error {
	const op = "TrialBegin"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || actor == "" {
		return newErr(op, ErrInvalidParam, "票号或操作人非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, actor); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if actor != p.Applicant {
		return newErr(op, ErrPermission, "试运行须由持票人 %s 申请", p.Applicant)
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateStarted {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不可进入试运行", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if len(p.onSite) > 0 {
		return newErr(op, ErrPrecondition, "仍有 %d 人在现场，不可试运行", len(p.onSite))
	}

	// 接受：暂时解除本票全部锁并保留记录
	p.trialSaved = map[string]map[string]bool{}
	for pt, holders := range p.locks {
		p.trialSaved[pt] = map[string]bool{}
		for person := range holders {
			p.trialSaved[pt][person] = true
		}
	}
	for pt, holders := range p.trialSaved {
		for person := range holders {
			s.removeLock(p, person, pt)
		}
	}
	s.setState(p, StateTrialRun)
	s.accept(t, op, p.ID, actor, "试运行开始，本票锁暂时解除")
	return nil
}

// TrialEnd 试运行结束，进入恢复上锁阶段。
// 之后由原持锁人通过 Lock 逐个重新上锁，全部恢复后票回到已上锁状态，
// 须再次执行零能量验证才可重新开工。
func (s *System) TrialEnd(t int64, permitID int, actor string) error {
	const op = "TrialEnd"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || actor == "" {
		return newErr(op, ErrInvalidParam, "票号或操作人非法")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if _, err := s.getPerson(op, actor); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if actor != p.Applicant && !s.people[actor][RoleSupervisor] {
		return newErr(op, ErrPermission, "操作人 %s 不是该票申请人也不是主管", actor)
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateTrialRun {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，不在试运行中", p.ID, p.stateAt(t))
	}

	// 接受
	s.setState(p, StateTrialRestore)
	s.accept(t, op, p.ID, actor, "试运行结束，待恢复原持锁人上锁")
	return nil
}

// ForceUnlock 主管强制摘除逾期票上的锁。
// 须附非空理由，并经另一名不同主管确认；记入审计；
// 被摘除锁的人再次进入该票现场前须重新上锁。
func (s *System) ForceUnlock(t int64, permitID int, actor, confirmer, person, point, reason string) error {
	const op = "ForceUnlock"
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if permitID <= 0 || actor == "" || confirmer == "" || person == "" || point == "" {
		return newErr(op, ErrInvalidParam, "票号、主管、确认人、人员或隔离点非法")
	}
	if reason == "" {
		return newErr(op, ErrInvalidParam, "强制摘除必须附非空理由")
	}
	// 2. 时刻回退
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	// 3. 对象不存在
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	for _, id := range []string{actor, confirmer, person} {
		if _, err := s.getPerson(op, id); err != nil {
			return err
		}
	}
	if err := s.checkPointExists(op, point); err != nil {
		return err
	}
	// 4. 无权限或角色不符
	if !s.people[actor][RoleSupervisor] {
		return newErr(op, ErrPermission, "人员 %s 不具备主管角色", actor)
	}
	if !s.people[confirmer][RoleSupervisor] {
		return newErr(op, ErrPermission, "确认人 %s 不具备主管角色", confirmer)
	}
	if actor == confirmer {
		return newErr(op, ErrPermission, "确认人须为另一名不同的主管")
	}
	// 5. 状态不允许
	if p.stateAt(t) != StateOverdue {
		return newErr(op, ErrState, "工作票 %d 状态为 %s，仅逾期票可强制摘除", p.ID, p.stateAt(t))
	}
	// 7. 条件不满足
	if !p.locks[point][person] {
		return newErr(op, ErrPrecondition, "人员 %s 在隔离点 %s 上对本票没有锁", person, point)
	}

	// 接受
	s.removeLock(p, person, point)
	p.mustRelock[person] = true
	s.accept(t, op, p.ID, actor, fmt.Sprintf("强制摘除 %s@%s 确认人=%s 理由=%q", person, point, confirmer, reason))
	return nil
}
