// Package naive 是能量隔离工作票系统的朴素参考模型：
// 不维护任何索引或计数器，每次判定都全量扫描历史票与锁记录。
// 它独立实现同一套规格，用于与高性能实现（loto 包）做随机对照测试。
package naive

import (
	"fmt"
	"sort"

	"ontology/loto"
)

type lockRec struct {
	PermitID int
	Person   string
	Point    string
}

type permit struct {
	id         int
	applicant  string
	devices    []string
	typ        loto.WorkType
	start, end int64
	state      loto.State
	approvers  map[string]bool
	workers    map[string]bool
	onSite     map[string]bool
	mustRelock map[string]bool
	trialSaved []lockRec
}

// System 朴素模型系统。
type System struct {
	devices  map[string][]string
	points   map[string]bool
	people   map[string]map[loto.Role]bool
	permits  []*permit // 全量历史，永不删除
	locks    []lockRec // 现存锁
	clock    int64
	clockSet bool
	nextID   int
}

func New(cfg *loto.Config) *System {
	s := &System{
		devices: map[string][]string{},
		points:  map[string]bool{},
		people:  map[string]map[loto.Role]bool{},
		nextID:  1,
	}
	for d, pts := range cfg.DevicePoints {
		s.devices[d] = append([]string(nil), pts...)
		for _, p := range pts {
			s.points[p] = true
		}
	}
	return s
}

func (s *System) AddPerson(id string, roles ...loto.Role) {
	if s.people[id] == nil {
		s.people[id] = map[loto.Role]bool{}
	}
	for _, r := range roles {
		s.people[id][r] = true
	}
}

func (s *System) findPermit(id int) *permit {
	for _, p := range s.permits {
		if p.id == id {
			return p
		}
	}
	return nil
}

func (p *permit) pointsOf(devices map[string][]string) []string {
	set := map[string]bool{}
	for _, d := range p.devices {
		for _, pt := range devices[d] {
			set[pt] = true
		}
	}
	out := make([]string, 0, len(set))
	for pt := range set {
		out = append(out, pt)
	}
	sort.Strings(out)
	return out
}

func (p *permit) stateAt(t int64) loto.State {
	if p.state.Occupied() && p.state != loto.StateOverdue && t >= p.end {
		return loto.StateOverdue
	}
	return p.state
}

func (s *System) checkTime(op string, t int64) error {
	if t < 0 {
		return errOf(loto.ErrInvalidParam, op, "时刻不能为负")
	}
	if s.clockSet && t < s.clock {
		return errOf(loto.ErrTimeRegression, op, "时刻回退")
	}
	return nil
}

// 朴素模型直接复用 loto.Error 表达错误类别，保证对照测试按类别比较。
func errOf(kind loto.ErrKind, op, msg string) error {
	return &loto.Error{Kind: kind, Op: op, Msg: msg}
}

func (s *System) getPermit(op string, id int) (*permit, error) {
	if p := s.findPermit(id); p != nil {
		return p, nil
	}
	return nil, errOf(loto.ErrNotFound, op, fmt.Sprintf("工作票 %d 不存在", id))
}

func (s *System) getPerson(op, id string) error {
	if _, ok := s.people[id]; !ok {
		return errOf(loto.ErrNotFound, op, fmt.Sprintf("人员 %s 不存在", id))
	}
	return nil
}

func (s *System) getPoint(op, point string) error {
	if !s.points[point] {
		return errOf(loto.ErrNotFound, op, fmt.Sprintf("隔离点 %s 不存在", point))
	}
	return nil
}

func (s *System) hasLock(permitID int, person, point string) bool {
	for _, l := range s.locks {
		if l.PermitID == permitID && l.Person == person && l.Point == point {
			return true
		}
	}
	return false
}

func (s *System) addLock(permitID int, person, point string) {
	s.locks = append(s.locks, lockRec{PermitID: permitID, Person: person, Point: point})
}

func (s *System) removeLock(permitID int, person, point string) {
	for i, l := range s.locks {
		if l.PermitID == permitID && l.Person == person && l.Point == point {
			s.locks = append(s.locks[:i], s.locks[i+1:]...)
			return
		}
	}
}

func (s *System) locksOfPermit(permitID int) []lockRec {
	var out []lockRec
	for _, l := range s.locks {
		if l.PermitID == permitID {
			out = append(out, l)
		}
	}
	return out
}

func (s *System) locksComplete(p *permit) bool {
	pts := p.pointsOf(s.devices)
	for w := range p.workers {
		for _, pt := range pts {
			if !s.hasLock(p.id, w, pt) {
				return false
			}
		}
	}
	return true
}

func (s *System) refreshLockState(p *permit) {
	if p.state != loto.StateEffective && p.state != loto.StateLocked {
		return
	}
	if s.locksComplete(p) {
		p.state = loto.StateLocked
	} else {
		p.state = loto.StateEffective
	}
}

// Apply 申请工作票。
func (s *System) Apply(t int64, applicant string, devices []string, wt loto.WorkType, start, end int64) (int, error) {
	const op = "Apply"
	if applicant == "" {
		return 0, errOf(loto.ErrInvalidParam, op, "申请人不能为空")
	}
	if len(devices) == 0 {
		return 0, errOf(loto.ErrInvalidParam, op, "设备集合不能为空")
	}
	seen := map[string]bool{}
	for _, d := range devices {
		if d == "" {
			return 0, errOf(loto.ErrInvalidParam, op, "设备编号不能为空")
		}
		if seen[d] {
			return 0, errOf(loto.ErrInvalidParam, op, "设备重复")
		}
		seen[d] = true
	}
	if wt != loto.WorkNormal && wt != loto.WorkHighRisk && wt != loto.WorkReadOnly {
		return 0, errOf(loto.ErrInvalidParam, op, "非法作业类型")
	}
	if start < 0 || end <= start {
		return 0, errOf(loto.ErrInvalidParam, op, "计划时段非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return 0, err
	}
	if err := s.getPerson(op, applicant); err != nil {
		return 0, err
	}
	for _, d := range devices {
		if _, ok := s.devices[d]; !ok {
			return 0, errOf(loto.ErrNotFound, op, "设备不存在")
		}
	}
	if !s.people[applicant][loto.RoleApplicant] {
		return 0, errOf(loto.ErrPermission, op, "不具备申请人角色")
	}
	devs := append([]string(nil), devices...)
	sort.Strings(devs)
	p := &permit{
		id:         s.nextID,
		applicant:  applicant,
		devices:    devs,
		typ:        wt,
		start:      start,
		end:        end,
		state:      loto.StateApplied,
		approvers:  map[string]bool{},
		workers:    map[string]bool{},
		onSite:     map[string]bool{},
		mustRelock: map[string]bool{},
	}
	s.permits = append(s.permits, p)
	s.nextID++
	s.clock, s.clockSet = t, true
	return p.id, nil
}

func approvalsNeeded(p *permit) int {
	if p.typ == loto.WorkHighRisk {
		return 2
	}
	return 1
}

// Approve 批准工作票。冲突在全量历史票中扫描判定（仅占用态参与）。
func (s *System) Approve(t int64, approver string, permitID int) error {
	const op = "Approve"
	if approver == "" || permitID <= 0 {
		return errOf(loto.ErrInvalidParam, op, "批准人或票号非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, approver); err != nil {
		return err
	}
	if !s.people[approver][loto.RoleApprover] {
		return errOf(loto.ErrPermission, op, "不具备批准人角色")
	}
	if approver == p.applicant {
		return errOf(loto.ErrPermission, op, "批准人不得是申请人")
	}
	if p.stateAt(t) != loto.StateApplied {
		return errOf(loto.ErrState, op, "状态不可批准")
	}
	if p.approvers[approver] {
		return errOf(loto.ErrPrecondition, op, "批准人重复批准")
	}
	wouldTakeEffect := len(p.approvers)+1 >= approvalsNeeded(p)
	if wouldTakeEffect {
		if c := s.findConflict(p); c != nil {
			return errOf(loto.ErrConflict, op, fmt.Sprintf("与工作票 %d 冲突", c.id))
		}
	}
	p.approvers[approver] = true
	if wouldTakeEffect {
		p.state = loto.StateEffective
		s.refreshLockState(p)
	}
	s.clock, s.clockSet = t, true
	return nil
}

func (s *System) findConflict(p *permit) *permit {
	for _, q := range s.permits { // 全量扫描历史票
		if q.id == p.id || !q.state.Occupied() {
			continue
		}
		if p.typ == loto.WorkReadOnly && q.typ == loto.WorkReadOnly {
			continue
		}
		if p.start >= q.end || q.start >= p.end {
			continue
		}
		for _, d1 := range p.devices {
			for _, d2 := range q.devices {
				if d1 == d2 {
					return q
				}
			}
		}
	}
	return nil
}

// AddWorker 登记作业人员。
func (s *System) AddWorker(t int64, actor string, permitID int, worker string) error {
	const op = "AddWorker"
	if actor == "" || worker == "" || permitID <= 0 {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, actor); err != nil {
		return err
	}
	if err := s.getPerson(op, worker); err != nil {
		return err
	}
	if actor != p.applicant && !s.people[actor][loto.RoleSupervisor] {
		return errOf(loto.ErrPermission, op, "操作人不是申请人也不是主管")
	}
	if !s.people[worker][loto.RoleWorker] {
		return errOf(loto.ErrPermission, op, "不具备作业人员角色")
	}
	switch p.stateAt(t) {
	case loto.StateApplied, loto.StateEffective, loto.StateLocked:
	default:
		return errOf(loto.ErrState, op, "状态不可登记作业人员")
	}
	if p.workers[worker] {
		return errOf(loto.ErrPrecondition, op, "作业人员已登记")
	}
	p.workers[worker] = true
	s.refreshLockState(p)
	s.clock, s.clockSet = t, true
	return nil
}

// Lock 上锁。
func (s *System) Lock(t int64, permitID int, person, point string) error {
	const op = "Lock"
	if permitID <= 0 || person == "" || point == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, person); err != nil {
		return err
	}
	if err := s.getPoint(op, point); err != nil {
		return err
	}
	if !p.workers[person] {
		return errOf(loto.ErrPermission, op, "不是登记作业人员")
	}
	switch p.stateAt(t) {
	case loto.StateEffective, loto.StateLocked, loto.StateTrialRestore, loto.StateOverdue:
	default:
		return errOf(loto.ErrState, op, "状态不可上锁")
	}
	inSet := false
	for _, pt := range p.pointsOf(s.devices) {
		if pt == point {
			inSet = true
			break
		}
	}
	if !inSet {
		return errOf(loto.ErrPrecondition, op, "隔离点不属于该票")
	}
	if s.hasLock(permitID, person, point) {
		return errOf(loto.ErrPrecondition, op, "重复上锁")
	}
	if p.state == loto.StateTrialRestore {
		wasSaved := false
		for _, l := range p.trialSaved {
			if l.Person == person && l.Point == point {
				wasSaved = true
				break
			}
		}
		if !wasSaved {
			return errOf(loto.ErrPrecondition, op, "不是原持锁人")
		}
	}
	s.addLock(permitID, person, point)
	if p.mustRelock[person] && s.personLocksComplete(p, person) {
		delete(p.mustRelock, person)
	}
	if p.state == loto.StateTrialRestore {
		kept := p.trialSaved[:0]
		for _, l := range p.trialSaved {
			if !(l.Person == person && l.Point == point) {
				kept = append(kept, l)
			}
		}
		p.trialSaved = kept
		if len(p.trialSaved) == 0 {
			p.state = loto.StateLocked
		}
	} else {
		s.refreshLockState(p)
	}
	s.clock, s.clockSet = t, true
	return nil
}

func (s *System) personLocksComplete(p *permit, person string) bool {
	for _, pt := range p.pointsOf(s.devices) {
		if !s.hasLock(p.id, person, pt) {
			return false
		}
	}
	return true
}

// Verify 零能量验证。
func (s *System) Verify(t int64, permitID int, verifier string) error {
	const op = "Verify"
	if permitID <= 0 || verifier == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, verifier); err != nil {
		return err
	}
	if p.stateAt(t) != loto.StateLocked {
		return errOf(loto.ErrState, op, "状态不可验证")
	}
	if p.workers[verifier] {
		return errOf(loto.ErrPrecondition, op, "验证人是该票作业人员")
	}
	if !s.people[verifier][loto.RoleApprover] && !s.people[verifier][loto.RoleSupervisor] {
		return errOf(loto.ErrPrecondition, op, "验证人不具备验证资格")
	}
	p.state = loto.StateVerified
	s.clock, s.clockSet = t, true
	return nil
}

// Start 开工。
func (s *System) Start(t int64, permitID int, actor string) error {
	const op = "Start"
	if permitID <= 0 || actor == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, actor); err != nil {
		return err
	}
	if actor != p.applicant && !s.people[actor][loto.RoleSupervisor] {
		return errOf(loto.ErrPermission, op, "操作人不是申请人也不是主管")
	}
	if p.stateAt(t) != loto.StateVerified {
		return errOf(loto.ErrState, op, "状态不可开工")
	}
	if t < p.start {
		return errOf(loto.ErrPrecondition, op, "开工时刻早于计划起点")
	}
	if t >= p.end {
		return errOf(loto.ErrPrecondition, op, "开工时刻不在计划时段内")
	}
	p.state = loto.StateStarted
	s.clock, s.clockSet = t, true
	return nil
}

// Enter 进入现场。
func (s *System) Enter(t int64, permitID int, person string) error {
	const op = "Enter"
	if permitID <= 0 || person == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, person); err != nil {
		return err
	}
	if !p.workers[person] {
		return errOf(loto.ErrPermission, op, "不是登记作业人员")
	}
	if p.stateAt(t) != loto.StateStarted {
		return errOf(loto.ErrState, op, "状态不可进入现场")
	}
	if p.onSite[person] {
		return errOf(loto.ErrPrecondition, op, "已在现场")
	}
	if !s.personLocksComplete(p, person) {
		return errOf(loto.ErrPrecondition, op, "未完成自己的全部上锁")
	}
	if p.mustRelock[person] {
		return errOf(loto.ErrPrecondition, op, "锁曾被强制摘除，须重新上锁")
	}
	p.onSite[person] = true
	s.clock, s.clockSet = t, true
	return nil
}

// Leave 离开现场。
func (s *System) Leave(t int64, permitID int, person string) error {
	const op = "Leave"
	if permitID <= 0 || person == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, person); err != nil {
		return err
	}
	if !p.workers[person] {
		return errOf(loto.ErrPermission, op, "不是登记作业人员")
	}
	if st := p.stateAt(t); st != loto.StateStarted && st != loto.StateOverdue {
		return errOf(loto.ErrState, op, "状态不可离场")
	}
	if !p.onSite[person] {
		return errOf(loto.ErrPrecondition, op, "不在现场")
	}
	delete(p.onSite, person)
	s.clock, s.clockSet = t, true
	return nil
}

// Complete 完工。
func (s *System) Complete(t int64, permitID int, actor string) error {
	const op = "Complete"
	if permitID <= 0 || actor == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, actor); err != nil {
		return err
	}
	if actor != p.applicant && !s.people[actor][loto.RoleSupervisor] {
		return errOf(loto.ErrPermission, op, "操作人不是申请人也不是主管")
	}
	if st := p.stateAt(t); st != loto.StateStarted && st != loto.StateOverdue {
		return errOf(loto.ErrState, op, "状态不可完工")
	}
	if len(p.onSite) > 0 {
		return errOf(loto.ErrPrecondition, op, "仍有人在现场")
	}
	p.state = loto.StateCompleted
	s.clock, s.clockSet = t, true
	return nil
}

// Unlock 摘锁。
func (s *System) Unlock(t int64, permitID int, person, point string) error {
	const op = "Unlock"
	if permitID <= 0 || person == "" || point == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, person); err != nil {
		return err
	}
	if err := s.getPoint(op, point); err != nil {
		return err
	}
	heldByOther := false
	for _, l := range s.locksOfPermit(permitID) {
		if l.Point == point && l.Person != person {
			heldByOther = true
			break
		}
	}
	if heldByOther && !s.hasLock(permitID, person, point) {
		return errOf(loto.ErrPermission, op, "锁由他人持有")
	}
	if p.stateAt(t) != loto.StateCompleted {
		return errOf(loto.ErrState, op, "完工后才允许摘锁")
	}
	if !s.hasLock(permitID, person, point) {
		return errOf(loto.ErrPrecondition, op, "本人无锁")
	}
	s.removeLock(permitID, person, point)
	s.clock, s.clockSet = t, true
	return nil
}

// TrialBegin 申请试运行：暂时解除本票全部锁并保留记录。
func (s *System) TrialBegin(t int64, permitID int, actor string) error {
	const op = "TrialBegin"
	if permitID <= 0 || actor == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, actor); err != nil {
		return err
	}
	if actor != p.applicant {
		return errOf(loto.ErrPermission, op, "试运行须由持票人申请")
	}
	if p.stateAt(t) != loto.StateStarted {
		return errOf(loto.ErrState, op, "状态不可试运行")
	}
	if len(p.onSite) > 0 {
		return errOf(loto.ErrPrecondition, op, "仍有人在现场")
	}
	p.trialSaved = s.locksOfPermit(permitID)
	for _, l := range p.trialSaved {
		s.removeLock(l.PermitID, l.Person, l.Point)
	}
	p.state = loto.StateTrialRun
	s.clock, s.clockSet = t, true
	return nil
}

// TrialEnd 试运行结束，进入恢复上锁阶段。
func (s *System) TrialEnd(t int64, permitID int, actor string) error {
	const op = "TrialEnd"
	if permitID <= 0 || actor == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	if err := s.getPerson(op, actor); err != nil {
		return err
	}
	if actor != p.applicant && !s.people[actor][loto.RoleSupervisor] {
		return errOf(loto.ErrPermission, op, "操作人不是申请人也不是主管")
	}
	if p.stateAt(t) != loto.StateTrialRun {
		return errOf(loto.ErrState, op, "不在试运行中")
	}
	p.state = loto.StateTrialRestore
	s.clock, s.clockSet = t, true
	return nil
}

// ForceUnlock 主管强制摘除逾期票上的锁。
func (s *System) ForceUnlock(t int64, permitID int, actor, confirmer, person, point, reason string) error {
	const op = "ForceUnlock"
	if permitID <= 0 || actor == "" || confirmer == "" || person == "" || point == "" {
		return errOf(loto.ErrInvalidParam, op, "参数非法")
	}
	if reason == "" {
		return errOf(loto.ErrInvalidParam, op, "强制摘除必须附非空理由")
	}
	if err := s.checkTime(op, t); err != nil {
		return err
	}
	p, err := s.getPermit(op, permitID)
	if err != nil {
		return err
	}
	for _, id := range []string{actor, confirmer, person} {
		if err := s.getPerson(op, id); err != nil {
			return err
		}
	}
	if err := s.getPoint(op, point); err != nil {
		return err
	}
	if !s.people[actor][loto.RoleSupervisor] {
		return errOf(loto.ErrPermission, op, "不具备主管角色")
	}
	if !s.people[confirmer][loto.RoleSupervisor] {
		return errOf(loto.ErrPermission, op, "确认人不具备主管角色")
	}
	if actor == confirmer {
		return errOf(loto.ErrPermission, op, "确认人须为另一名不同的主管")
	}
	if p.stateAt(t) != loto.StateOverdue {
		return errOf(loto.ErrState, op, "仅逾期票可强制摘除")
	}
	if !s.hasLock(permitID, person, point) {
		return errOf(loto.ErrPrecondition, op, "该人员在此隔离点上无锁")
	}
	s.removeLock(permitID, person, point)
	p.mustRelock[person] = true
	s.clock, s.clockSet = t, true
	return nil
}

// PermitState 查询票状态。
func (s *System) PermitState(id int) (loto.State, error) {
	p := s.findPermit(id)
	if p == nil {
		return 0, errOf(loto.ErrNotFound, "PermitState", "工作票不存在")
	}
	return p.stateAt(s.clock), nil
}

// EnergizableAt 送电判定：全量扫描历史票与锁记录。
func (s *System) EnergizableAt(device string, t int64) (loto.Decision, error) {
	const op = "Energizable"
	if device == "" {
		return loto.Decision{}, errOf(loto.ErrInvalidParam, op, "设备编号不能为空")
	}
	if _, ok := s.devices[device]; !ok {
		return loto.Decision{}, errOf(loto.ErrNotFound, op, "设备不存在")
	}
	if t < s.clock {
		t = s.clock
	}
	d := loto.Decision{Device: device}
	// 锁计数：该设备依赖隔离点上的全部锁
	devPoints := map[string]bool{}
	for _, pt := range s.devices[device] {
		devPoints[pt] = true
	}
	for _, l := range s.locks {
		if devPoints[l.Point] {
			d.Locks++
		}
	}
	// 阻止送电的占用态票：全量扫描
	for _, p := range s.permits {
		if !p.stateAt(t).Occupied() {
			continue
		}
		st := p.stateAt(t)
		if st == loto.StateStarted || st == loto.StateTrialRun {
			continue
		}
		involves := false
		for _, dv := range p.devices {
			if dv == device {
				involves = true
				break
			}
		}
		if involves {
			d.Blocking = append(d.Blocking, p.id)
		}
	}
	sort.Ints(d.Blocking)
	d.OK = d.Locks == 0 && len(d.Blocking) == 0
	if d.OK {
		d.Reason = "全部隔离点无锁，且无开工/试运行之外的占用态票"
	} else {
		d.Reason = "存在锁或阻止送电的占用态票"
	}
	return d, nil
}
