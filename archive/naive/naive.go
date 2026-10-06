// Package naive 是 archive 服务的独立朴素参考实现，
// 仅用于差分测试对照。语义与 archive 包文档一致，
// 但采用最简单的数据结构与“整状态克隆、拒绝即丢弃”策略。
package naive

import (
	"sort"

	"ontology/archive"
)

type loan struct {
	borrowerID string
	borrowDay  int
	dueDay     int
	renews     int
}

type assignment struct {
	borrowerID string
	assignDay  int
	deadline   int
}

type volume struct {
	id         string
	level      archive.SecretLevel
	status     archive.VolumeStatus
	loan       *loan
	assignment *assignment
	queue      []string // 等待中的预约借阅人，按提交先后
}

type borrower struct {
	id            string
	maxLevel      archive.SecretLevel
	suspended     bool
	cumOverdue    int
	activeLoans   int
	lastReturn    int
	hasLastReturn bool
}

// Service 朴素服务实现。每个操作先整体克隆状态，
// 在副本上执行，被拒绝则直接丢弃副本，天然满足“拒绝不留痕”。
type Service struct {
	cfg       archive.Config
	volumes   map[string]*volume
	borrowers map[string]*borrower
	watermark int
	hasClock  bool
}

// New 创建朴素服务。
func New(cfg archive.Config) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Service{
		cfg:       cfg,
		volumes:   map[string]*volume{},
		borrowers: map[string]*borrower{},
	}, nil
}

func (s *Service) clone() *Service {
	c := &Service{
		cfg:       s.cfg,
		volumes:   make(map[string]*volume, len(s.volumes)),
		borrowers: make(map[string]*borrower, len(s.borrowers)),
		watermark: s.watermark,
		hasClock:  s.hasClock,
	}
	for id, v := range s.volumes {
		nv := &volume{id: v.id, level: v.level, status: v.status}
		if v.loan != nil {
			l := *v.loan
			nv.loan = &l
		}
		if v.assignment != nil {
			a := *v.assignment
			nv.assignment = &a
		}
		nv.queue = append([]string(nil), v.queue...)
		c.volumes[id] = nv
	}
	for id, b := range s.borrowers {
		nb := *b
		c.borrowers[id] = &nb
	}
	return c
}

func (s *Service) effSuspended(b *borrower, now int) bool {
	if !b.suspended {
		return false
	}
	if b.activeLoans == 0 && b.hasLastReturn && now-b.lastReturn >= s.cfg.CooldownDays {
		return false
	}
	return true
}

func (s *Service) effCumOverdue(b *borrower, now int) int {
	if b.suspended && !s.effSuspended(b, now) {
		return 0
	}
	return b.cumOverdue
}

func (s *Service) syncBorrower(b *borrower, now int) {
	if b.suspended && !s.effSuspended(b, now) {
		b.suspended = false
		b.cumOverdue = 0
	}
}

// refresh 懒惰处理取卷期限届满并重新扫描队首分配（朴素全扫）。
func (s *Service) refresh(v *volume, now int) {
	if v.assignment == nil || now <= v.assignment.deadline {
		return
	}
	v.assignment = nil
	s.assignFromHead(v, now)
}

// assignFromHead 从队首起找到第一个密级满足且未暂停的预约者并分配。
func (s *Service) assignFromHead(v *volume, now int) {
	for i, id := range v.queue {
		b := s.borrowers[id]
		if b == nil {
			continue
		}
		if b.maxLevel >= v.level && !s.effSuspended(b, now) {
			v.assignment = &assignment{
				borrowerID: id,
				assignDay:  now,
				deadline:   now + s.cfg.PickupDays,
			}
			v.queue = append(v.queue[:i], v.queue[i+1:]...)
			return
		}
	}
}

// Apply 执行一个操作。参数与时钟校验在原状态上进行；
// 业务执行在整体克隆的副本上进行，被拒绝则丢弃副本。
func (s *Service) Apply(op archive.Op) archive.Result {
	if err := checkParams(op); err != nil {
		return archive.Result{Err: err}
	}
	if s.hasClock && op.Now < s.watermark {
		return archive.Result{Err: &archive.Error{
			Code:  archive.ErrClockRollback,
			Index: -1,
			Msg:   "时钟回退",
		}}
	}
	c := s.clone()
	res := c.exec(op)
	if res.Err != nil {
		return res
	}
	c.watermark = op.Now
	c.hasClock = true
	*s = *c
	return res
}

func checkParams(op archive.Op) *archive.Error {
	if op.Now < 0 {
		return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "now 为负"}
	}
	switch op.Kind {
	case archive.OpAddVolume:
		if op.VolumeID == "" || !validLevel(op.Level) {
			return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "卷参数非法"}
		}
	case archive.OpAddBorrower, archive.OpSetBorrowerLevel:
		if op.BorrowerID == "" || !validLevel(op.Level) {
			return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "借阅人参数非法"}
		}
	case archive.OpSeal, archive.OpReturn:
		if op.VolumeID == "" {
			return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "卷标识为空"}
		}
	case archive.OpBorrow:
		if op.BorrowerID == "" || len(op.VolumeIDs) == 0 {
			return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "借阅参数非法"}
		}
		seen := map[string]bool{}
		for i, id := range op.VolumeIDs {
			if id == "" || seen[id] {
				return &archive.Error{Code: archive.ErrInvalidParam, Index: i, Msg: "卷标识为空或批内重复"}
			}
			seen[id] = true
		}
	case archive.OpReserve, archive.OpPickup, archive.OpRenew:
		if op.BorrowerID == "" || op.VolumeID == "" {
			return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "标识为空"}
		}
	default:
		return &archive.Error{Code: archive.ErrInvalidParam, Index: -1, Msg: "未知操作"}
	}
	return nil
}

func validLevel(l archive.SecretLevel) bool {
	return l >= archive.Public && l <= archive.TopSecret
}

func errOf(code archive.ErrCode, index int, msg string) archive.Result {
	return archive.Result{Err: &archive.Error{Code: code, Index: index, Msg: msg}}
}

func (s *Service) exec(op archive.Op) archive.Result {
	switch op.Kind {
	case archive.OpAddVolume:
		return s.execAddVolume(op)
	case archive.OpAddBorrower:
		return s.execAddBorrower(op)
	case archive.OpSetBorrowerLevel:
		return s.execSetBorrowerLevel(op)
	case archive.OpSeal:
		return s.execSeal(op)
	case archive.OpBorrow:
		return s.execBorrow(op)
	case archive.OpReserve:
		return s.execReserve(op)
	case archive.OpReturn:
		return s.execReturn(op)
	case archive.OpRenew:
		return s.execRenew(op)
	case archive.OpPickup:
		return s.execPickup(op)
	}
	return errOf(archive.ErrInvalidParam, -1, "未知操作")
}

func (s *Service) execAddVolume(op archive.Op) archive.Result {
	if _, dup := s.volumes[op.VolumeID]; dup {
		return errOf(archive.ErrInvalidState, -1, "卷已存在")
	}
	s.volumes[op.VolumeID] = &volume{id: op.VolumeID, level: op.Level, status: archive.StatusAvailable}
	return archive.Result{}
}

func (s *Service) execAddBorrower(op archive.Op) archive.Result {
	if _, dup := s.borrowers[op.BorrowerID]; dup {
		return errOf(archive.ErrInvalidState, -1, "借阅人已存在")
	}
	s.borrowers[op.BorrowerID] = &borrower{id: op.BorrowerID, maxLevel: op.Level}
	return archive.Result{}
}

func (s *Service) execSetBorrowerLevel(op archive.Op) archive.Result {
	b := s.borrowers[op.BorrowerID]
	if b == nil {
		return errOf(archive.ErrNotFound, -1, "借阅人不存在")
	}
	b.maxLevel = op.Level
	return archive.Result{}
}

func (s *Service) execSeal(op archive.Op) archive.Result {
	v := s.volumes[op.VolumeID]
	if v == nil {
		return errOf(archive.ErrNotFound, -1, "卷不存在")
	}
	if v.status == archive.StatusSealed {
		return errOf(archive.ErrInvalidState, -1, "卷已封存")
	}
	v.status = archive.StatusSealed
	if v.loan == nil {
		v.assignment = nil
		v.queue = nil
	}
	return archive.Result{}
}

func (s *Service) execBorrow(op archive.Op) archive.Result {
	b := s.borrowers[op.BorrowerID]
	if b == nil {
		return errOf(archive.ErrNotFound, -1, "借阅人不存在")
	}
	for _, id := range op.VolumeIDs {
		if v := s.volumes[id]; v != nil {
			s.refresh(v, op.Now)
		}
	}
	for i, id := range op.VolumeIDs {
		if s.volumes[id] == nil {
			return errOf(archive.ErrNotFound, i, "卷不存在")
		}
	}
	for i, id := range op.VolumeIDs {
		if s.volumes[id].status == archive.StatusSealed {
			return errOf(archive.ErrInvalidState, i, "卷已封存")
		}
	}
	for i, id := range op.VolumeIDs {
		if b.maxLevel < s.volumes[id].level {
			return errOf(archive.ErrClearance, i, "密级不足")
		}
	}
	if s.effSuspended(b, op.Now) {
		return errOf(archive.ErrSuspended, -1, "借阅人暂停")
	}
	for i, id := range op.VolumeIDs {
		v := s.volumes[id]
		if v.status == archive.StatusLent || v.assignment != nil {
			return errOf(archive.ErrBorrowed, i, "卷已借出")
		}
	}
	s.syncBorrower(b, op.Now)
	out := make([]archive.BorrowResult, 0, len(op.VolumeIDs))
	for _, id := range op.VolumeIDs {
		v := s.volumes[id]
		due := op.Now + s.cfg.LoanDays[v.level]
		v.status = archive.StatusLent
		v.loan = &loan{borrowerID: b.id, borrowDay: op.Now, dueDay: due}
		b.activeLoans++
		out = append(out, archive.BorrowResult{VolumeID: id, DueDay: due})
	}
	return archive.Result{BorrowResults: out}
}

func (s *Service) execReserve(op archive.Op) archive.Result {
	v := s.volumes[op.VolumeID]
	b := s.borrowers[op.BorrowerID]
	if v != nil {
		s.refresh(v, op.Now)
	}
	if v == nil {
		return errOf(archive.ErrNotFound, -1, "卷不存在")
	}
	if b == nil {
		return errOf(archive.ErrNotFound, -1, "借阅人不存在")
	}
	if v.status == archive.StatusSealed {
		return errOf(archive.ErrInvalidState, -1, "卷已封存")
	}
	if v.status == archive.StatusAvailable && v.assignment == nil {
		return errOf(archive.ErrInvalidState, -1, "卷在库可直接借阅")
	}
	for _, id := range v.queue {
		if id == b.id {
			return errOf(archive.ErrInvalidState, -1, "重复预约")
		}
	}
	if v.assignment != nil && v.assignment.borrowerID == b.id {
		return errOf(archive.ErrInvalidState, -1, "重复预约")
	}
	if b.maxLevel < v.level {
		return errOf(archive.ErrClearance, -1, "密级不足")
	}
	if s.effSuspended(b, op.Now) {
		return errOf(archive.ErrSuspended, -1, "借阅人暂停")
	}
	s.syncBorrower(b, op.Now)
	v.queue = append(v.queue, b.id)
	return archive.Result{}
}

func (s *Service) execReturn(op archive.Op) archive.Result {
	v := s.volumes[op.VolumeID]
	if v == nil {
		return errOf(archive.ErrNotFound, -1, "卷不存在")
	}
	l := v.loan
	if l == nil {
		return errOf(archive.ErrInvalidState, -1, "卷未借出")
	}
	b := s.borrowers[l.borrowerID]
	overdue := 0
	if op.Now > l.dueDay {
		overdue = op.Now - l.dueDay
	}
	v.loan = nil
	b.activeLoans--
	b.cumOverdue += overdue
	b.lastReturn = op.Now
	b.hasLastReturn = true
	if b.cumOverdue >= s.cfg.OverdueThreshold {
		b.suspended = true
	}
	if v.status == archive.StatusSealed {
		v.queue = nil
	} else {
		v.status = archive.StatusAvailable
		s.assignFromHead(v, op.Now)
	}
	return archive.Result{OverdueDays: overdue}
}

func (s *Service) execRenew(op archive.Op) archive.Result {
	v := s.volumes[op.VolumeID]
	if v == nil {
		return errOf(archive.ErrNotFound, -1, "卷不存在")
	}
	b := s.borrowers[op.BorrowerID]
	if b == nil {
		return errOf(archive.ErrNotFound, -1, "借阅人不存在")
	}
	l := v.loan
	if l == nil || l.borrowerID != b.id {
		return errOf(archive.ErrInvalidState, -1, "卷未借出给该借阅人")
	}
	if s.effSuspended(b, op.Now) {
		return errOf(archive.ErrSuspended, -1, "借阅人暂停")
	}
	if l.renews >= s.cfg.MaxRenews {
		return errOf(archive.ErrRenew, -1, "续借超限")
	}
	windowStart := l.dueDay - (s.cfg.RenewWindowDays - 1)
	if op.Now < windowStart {
		return errOf(archive.ErrRenew, -1, "续借窗口未到")
	}
	if op.Now > l.dueDay {
		return errOf(archive.ErrRenew, -1, "续借窗口已过")
	}
	if len(v.queue) > 0 {
		return errOf(archive.ErrReservation, -1, "存在有效预约")
	}
	if v.level >= archive.Confidential {
		if op.ApproverID == "" {
			return errOf(archive.ErrInvalidParam, -1, "需审批人")
		}
		if op.ApproverID == b.id {
			return errOf(archive.ErrInvalidParam, -1, "审批人不得是本人")
		}
		if s.borrowers[op.ApproverID] == nil {
			return errOf(archive.ErrNotFound, -1, "审批人不存在")
		}
	}
	l.renews++
	l.dueDay = l.dueDay + 1 + s.cfg.LoanDays[v.level]
	return archive.Result{NewDueDay: l.dueDay}
}

func (s *Service) execPickup(op archive.Op) archive.Result {
	v := s.volumes[op.VolumeID]
	b := s.borrowers[op.BorrowerID]
	if v != nil {
		s.refresh(v, op.Now)
	}
	if v == nil {
		return errOf(archive.ErrNotFound, -1, "卷不存在")
	}
	if b == nil {
		return errOf(archive.ErrNotFound, -1, "借阅人不存在")
	}
	if v.assignment == nil {
		return errOf(archive.ErrInvalidState, -1, "无待取分配")
	}
	if v.assignment.borrowerID != b.id {
		return errOf(archive.ErrInvalidState, -1, "卷已分配给其他人")
	}
	if v.status == archive.StatusSealed {
		return errOf(archive.ErrInvalidState, -1, "卷已封存")
	}
	if b.maxLevel < v.level {
		return errOf(archive.ErrClearance, -1, "密级不足")
	}
	if s.effSuspended(b, op.Now) {
		return errOf(archive.ErrSuspended, -1, "借阅人暂停")
	}
	s.syncBorrower(b, op.Now)
	v.assignment = nil
	due := op.Now + s.cfg.LoanDays[v.level]
	v.status = archive.StatusLent
	v.loan = &loan{borrowerID: b.id, borrowDay: op.Now, dueDay: due}
	b.activeLoans++
	return archive.Result{DueDay: due}
}

// Snapshot 返回 now 时刻的全量可观察状态，供差分对照。
func (s *Service) Snapshot(now int) archive.StateSnapshot {
	snap := archive.StateSnapshot{Watermark: s.watermark, HasClock: s.hasClock}
	vids := make([]string, 0, len(s.volumes))
	for id := range s.volumes {
		vids = append(vids, id)
	}
	sort.Strings(vids)
	for _, id := range vids {
		v := s.volumes[id]
		vs := archive.VolumeSnapshot{
			ID:     v.id,
			Level:  v.level,
			Status: v.status,
			Queue:  append([]string{}, v.queue...),
		}
		if v.loan != nil {
			vs.Loan = &archive.LoanSnapshot{
				BorrowerID: v.loan.borrowerID,
				BorrowDay:  v.loan.borrowDay,
				DueDay:     v.loan.dueDay,
				Renews:     v.loan.renews,
			}
		}
		if v.assignment != nil && now <= v.assignment.deadline {
			vs.Assignment = &archive.AssignmentSnapshot{
				BorrowerID: v.assignment.borrowerID,
				AssignDay:  v.assignment.assignDay,
				Deadline:   v.assignment.deadline,
			}
		}
		snap.Volumes = append(snap.Volumes, vs)
	}
	bids := make([]string, 0, len(s.borrowers))
	for id := range s.borrowers {
		bids = append(bids, id)
	}
	sort.Strings(bids)
	for _, id := range bids {
		b := s.borrowers[id]
		snap.Borrowers = append(snap.Borrowers, archive.BorrowerSnapshot{
			ID:            b.id,
			MaxLevel:      b.maxLevel,
			Suspended:     s.effSuspended(b, now),
			CumOverdue:    s.effCumOverdue(b, now),
			ActiveLoans:   b.activeLoans,
			LastReturn:    b.lastReturn,
			HasLastReturn: b.hasLastReturn,
		})
	}
	return snap
}
