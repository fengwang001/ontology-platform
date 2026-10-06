package archive

import (
	"sort"
	"sync"
)

// loan 一次在借记录。
type loan struct {
	borrower  *borrower
	borrowDay int
	dueDay    int
	renews    int
}

// assignment 归还后对待取卷者的分配。
type assignment struct {
	borrower  *borrower
	entry     *resvEntry // 被分配时已出队的节点，用于回滚
	assignDay int
	deadline  int
}

// volume 档案卷。
type volume struct {
	id         string
	level      SecretLevel
	status     VolumeStatus
	loan       *loan
	assignment *assignment
	queue      resvQueue
}

// borrower 借阅人。suspended/cumOverdue 为存储值，
// 有效值由 effSuspended/effCumOverdue 结合 now 推导（冷静期懒惰求值）。
type borrower struct {
	id            string
	maxLevel      SecretLevel
	suspended     bool
	cumOverdue    int
	activeLoans   int
	lastReturn    int
	hasLastReturn bool
}

// Service 借阅与预约服务。所有公开方法持有同一把互斥锁，
// 因此并发调用线性化，等价于某个串行顺序。
type Service struct {
	mu        sync.Mutex
	cfg       Config
	volumes   map[string]*volume
	borrowers map[string]*borrower
	seq       int64 // 全局单调序号，用于预约排队
	watermark int   // 最近一次被接受操作的 now
	hasClock  bool
	stats     Stats
	hook      func(Op, Result)
}

// New 创建服务；配置非法时返回错误。
func New(cfg Config) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Service{
		cfg:       cfg,
		volumes:   make(map[string]*volume),
		borrowers: make(map[string]*borrower),
	}, nil
}

// SetHook 设置在锁内、每个操作完成后回调的钩子（用于日志与重放验证）。
func (s *Service) SetHook(h func(Op, Result)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = h
}

// Stats 返回运行统计。
func (s *Service) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// effSuspended 推导 now 时刻的有效暂停状态：
// 全部在借卷已归还且自最近一次归还起满冷静天数（恰等即解除）时解除。
func (s *Service) effSuspended(b *borrower, now int) bool {
	if !b.suspended {
		return false
	}
	if b.activeLoans == 0 && b.hasLastReturn && now-b.lastReturn >= s.cfg.CooldownDays {
		return false
	}
	return true
}

// effCumOverdue 推导 now 时刻的有效累计逾期天数（解除暂停时清零）。
func (s *Service) effCumOverdue(b *borrower, now int) int {
	if b.suspended && !s.effSuspended(b, now) {
		return 0
	}
	return b.cumOverdue
}

// syncBorrower 在接受操作时把已满足的暂停解除物化，保持存储值与推导值一致。
func (s *Service) syncBorrower(b *borrower, now int) {
	if b.suspended && !s.effSuspended(b, now) {
		b.suspended = false
		b.cumOverdue = 0
	}
}

// Apply 执行一个操作，是服务的唯一入口。
func (s *Service) Apply(op Op) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := s.applyLocked(op)
	if s.hook != nil {
		s.hook(op, res)
	}
	return res
}

func (s *Service) applyLocked(op Op) Result {
	if err := s.checkParams(op); err != nil {
		return Result{Err: err}
	}
	if s.hasClock && op.Now < s.watermark {
		return Result{Err: newErr(ErrClockRollback, -1,
			"now=%d 小于上一次被接受操作的 now=%d", op.Now, s.watermark)}
	}
	var res Result
	switch op.Kind {
	case OpAddVolume:
		res = s.execAddVolume(op)
	case OpAddBorrower:
		res = s.execAddBorrower(op)
	case OpSetBorrowerLevel:
		res = s.execSetBorrowerLevel(op)
	case OpSeal:
		res = s.execSeal(op)
	case OpBorrow:
		res = s.execBorrow(op)
	case OpReserve:
		res = s.execReserve(op)
	case OpReturn:
		res = s.execReturn(op)
	case OpRenew:
		res = s.execRenew(op)
	case OpPickup:
		res = s.execPickup(op)
	}
	if res.Err == nil {
		s.watermark = op.Now
		s.hasClock = true
	}
	return res
}

// checkParams 参数非法校验（优先级最高，先于时钟检查）。
func (s *Service) checkParams(op Op) *Error {
	if op.Now < 0 {
		return newErr(ErrInvalidParam, -1, "now 不得为负: %d", op.Now)
	}
	switch op.Kind {
	case OpAddVolume:
		if op.VolumeID == "" {
			return newErr(ErrInvalidParam, -1, "卷标识为空")
		}
		if !op.Level.valid() {
			return newErr(ErrInvalidParam, -1, "非法密级: %d", int(op.Level))
		}
	case OpAddBorrower, OpSetBorrowerLevel:
		if op.BorrowerID == "" {
			return newErr(ErrInvalidParam, -1, "借阅人标识为空")
		}
		if !op.Level.valid() {
			return newErr(ErrInvalidParam, -1, "非法密级: %d", int(op.Level))
		}
	case OpSeal, OpReturn:
		if op.VolumeID == "" {
			return newErr(ErrInvalidParam, -1, "卷标识为空")
		}
	case OpBorrow:
		if op.BorrowerID == "" {
			return newErr(ErrInvalidParam, -1, "借阅人标识为空")
		}
		if len(op.VolumeIDs) == 0 {
			return newErr(ErrInvalidParam, -1, "批量借阅卷列表为空")
		}
		seen := make(map[string]int, len(op.VolumeIDs))
		for i, id := range op.VolumeIDs {
			if id == "" {
				return newErr(ErrInvalidParam, i, "卷标识为空")
			}
			if j, dup := seen[id]; dup {
				return newErr(ErrInvalidParam, i, "批内卷 %q 与下标 %d 重复", id, j)
			}
			seen[id] = i
		}
	case OpReserve, OpPickup:
		if op.BorrowerID == "" {
			return newErr(ErrInvalidParam, -1, "借阅人标识为空")
		}
		if op.VolumeID == "" {
			return newErr(ErrInvalidParam, -1, "卷标识为空")
		}
	case OpRenew:
		if op.BorrowerID == "" {
			return newErr(ErrInvalidParam, -1, "借阅人标识为空")
		}
		if op.VolumeID == "" {
			return newErr(ErrInvalidParam, -1, "卷标识为空")
		}
	default:
		return newErr(ErrInvalidParam, -1, "未知操作类别: %d", int(op.Kind))
	}
	return nil
}

// expiryUndo 记录一次期满处理的现场，用于被拒绝操作的回滚。
type expiryUndo struct {
	v             *volume
	oldAssignment *assignment
	removed       *resvEntry // 因再分配而出队的节点
	remPrev       *resvEntry // removed 原位置的前驱
}

// processExpiry 懒惰处理取卷期限届满：分配日起算 PickupDays 天，
// 期限最后一日仍可取，now 超过期限即视为放弃，失去排队位置，
// 卷继续分配给队首起第一个资格仍满足且状态正常的预约者。
func (s *Service) processExpiry(v *volume, now int) *expiryUndo {
	a := v.assignment
	if a == nil || now <= a.deadline {
		return nil
	}
	u := &expiryUndo{v: v, oldAssignment: a}
	v.assignment = nil
	if e := s.scanAssign(v, now); e != nil {
		u.removed = e
		u.remPrev = e.prev
		v.queue.remove(e)
		v.assignment = &assignment{
			borrower:  e.borrower,
			entry:     e,
			assignDay: now,
			deadline:  now + s.cfg.PickupDays,
		}
	}
	return u
}

// undoExpiry 回滚一次期满处理，恢复原分配与队列序位。
func undoExpiry(u *expiryUndo) {
	if u == nil {
		return
	}
	if u.removed != nil {
		u.v.assignment = nil
		u.v.queue.insertAfter(u.remPrev, u.removed)
	}
	u.v.assignment = u.oldAssignment
}

// scanAssign 从队首起找到第一个资格满足（密级不低于卷密级）
// 且状态正常（未暂停）的预约者；不满足的跳过但保留序位。
// 扫描只触及当前存活节点，开销与历史预约总数无关。
func (s *Service) scanAssign(v *volume, now int) *resvEntry {
	for e := v.queue.head; e != nil; e = e.next {
		s.stats.QueueScanSteps++
		b := e.borrower
		if b.maxLevel >= v.level && !s.effSuspended(b, now) {
			return e
		}
	}
	return nil
}

// assignHead 归还后的再分配：队首起第一个有效预约者获得分配。
func (s *Service) assignHead(v *volume, now int) {
	if e := s.scanAssign(v, now); e != nil {
		v.queue.remove(e)
		v.assignment = &assignment{
			borrower:  e.borrower,
			entry:     e,
			assignDay: now,
			deadline:  now + s.cfg.PickupDays,
		}
	}
}

func (s *Service) execAddVolume(op Op) Result {
	if _, dup := s.volumes[op.VolumeID]; dup {
		return Result{Err: newErr(ErrInvalidState, -1, "卷 %q 已存在", op.VolumeID)}
	}
	s.volumes[op.VolumeID] = &volume{id: op.VolumeID, level: op.Level, status: StatusAvailable}
	return Result{}
}

func (s *Service) execAddBorrower(op Op) Result {
	if _, dup := s.borrowers[op.BorrowerID]; dup {
		return Result{Err: newErr(ErrInvalidState, -1, "借阅人 %q 已存在", op.BorrowerID)}
	}
	s.borrowers[op.BorrowerID] = &borrower{id: op.BorrowerID, maxLevel: op.Level}
	return Result{}
}

func (s *Service) execSetBorrowerLevel(op Op) Result {
	b := s.borrowers[op.BorrowerID]
	if b == nil {
		return Result{Err: newErr(ErrNotFound, -1, "借阅人 %q 不存在", op.BorrowerID)}
	}
	b.maxLevel = op.Level
	return Result{}
}

// execSeal 封存：在库卷取消待取分配与全部预约；
// 已借出的封存后仍须归还，归还时不再分配并取消预约。
func (s *Service) execSeal(op Op) Result {
	v := s.volumes[op.VolumeID]
	if v == nil {
		return Result{Err: newErr(ErrNotFound, -1, "卷 %q 不存在", op.VolumeID)}
	}
	if v.status == StatusSealed {
		return Result{Err: newErr(ErrInvalidState, -1, "卷 %q 已处于封存状态", op.VolumeID)}
	}
	v.status = StatusSealed
	if v.loan == nil {
		v.assignment = nil
		v.queue.clear()
	}
	return Result{}
}

// execBorrow 多卷同借，全有或全无。逐项检查按错误类别优先级进行，
// 同一类别内报下标最小的失败项；任一失败则整批拒绝且不留痕。
func (s *Service) execBorrow(op Op) Result {
	b := s.borrowers[op.BorrowerID]
	if b == nil {
		return Result{Err: newErr(ErrNotFound, -1, "借阅人 %q 不存在", op.BorrowerID)}
	}
	// 对批内各卷先做期满懒惰处理；若整批被拒绝则回滚。
	var undos []*expiryUndo
	rollback := func() {
		for i := len(undos) - 1; i >= 0; i-- {
			undoExpiry(undos[i])
		}
	}
	for _, id := range op.VolumeIDs {
		if v := s.volumes[id]; v != nil {
			if u := s.processExpiry(v, op.Now); u != nil {
				undos = append(undos, u)
			}
		}
	}
	fail := func(e *Error) Result {
		rollback()
		return Result{Err: e}
	}
	for i, id := range op.VolumeIDs {
		if s.volumes[id] == nil {
			return fail(newErr(ErrNotFound, i, "卷 %q 不存在", id))
		}
	}
	for i, id := range op.VolumeIDs {
		if s.volumes[id].status == StatusSealed {
			return fail(newErr(ErrInvalidState, i, "卷 %q 已封存，不得借出", id))
		}
	}
	for i, id := range op.VolumeIDs {
		v := s.volumes[id]
		if b.maxLevel < v.level {
			return fail(newErr(ErrClearance, i,
				"借阅人 %q 最高密级%s 低于卷 %q 密级%s",
				b.id, b.maxLevel, id, v.level))
		}
	}
	if s.effSuspended(b, op.Now) {
		return fail(newErr(ErrSuspended, -1, "借阅人 %q 处于暂停状态", b.id))
	}
	for i, id := range op.VolumeIDs {
		v := s.volumes[id]
		if v.status == StatusLent || v.assignment != nil {
			return fail(newErr(ErrBorrowed, i, "卷 %q 已借出或已分配待取", id))
		}
	}
	s.syncBorrower(b, op.Now)
	out := make([]BorrowResult, 0, len(op.VolumeIDs))
	for _, id := range op.VolumeIDs {
		v := s.volumes[id]
		due := op.Now + s.cfg.LoanDays[v.level]
		v.status = StatusLent
		v.loan = &loan{borrower: b, borrowDay: op.Now, dueDay: due}
		b.activeLoans++
		out = append(out, BorrowResult{VolumeID: id, DueDay: due})
	}
	return Result{BorrowResults: out}
}

// execReserve 预约：仅借出中（含待取）的卷可预约，按提交先后排队，
// 同一人对同一卷不得重复预约。
func (s *Service) execReserve(op Op) Result {
	v := s.volumes[op.VolumeID]
	b := s.borrowers[op.BorrowerID]
	var undo *expiryUndo
	if v != nil {
		undo = s.processExpiry(v, op.Now)
	}
	fail := func(e *Error) Result {
		undoExpiry(undo)
		return Result{Err: e}
	}
	if v == nil {
		return fail(newErr(ErrNotFound, -1, "卷 %q 不存在", op.VolumeID))
	}
	if b == nil {
		return fail(newErr(ErrNotFound, -1, "借阅人 %q 不存在", op.BorrowerID))
	}
	if v.status == StatusSealed {
		return fail(newErr(ErrInvalidState, -1, "卷 %q 已封存，不得预约", v.id))
	}
	if v.status == StatusAvailable && v.assignment == nil {
		return fail(newErr(ErrInvalidState, -1, "卷 %q 在库，可直接借阅，无需预约", v.id))
	}
	if v.queue.contains(b) || (v.assignment != nil && v.assignment.borrower == b) {
		return fail(newErr(ErrInvalidState, -1, "借阅人 %q 已对卷 %q 有有效预约", b.id, v.id))
	}
	if b.maxLevel < v.level {
		return fail(newErr(ErrClearance, -1,
			"借阅人 %q 最高密级%s 低于卷 %q 密级%s", b.id, b.maxLevel, v.id, v.level))
	}
	if s.effSuspended(b, op.Now) {
		return fail(newErr(ErrSuspended, -1, "借阅人 %q 处于暂停状态", b.id))
	}
	s.syncBorrower(b, op.Now)
	s.seq++
	v.queue.pushBack(&resvEntry{seq: s.seq, borrower: b})
	return Result{}
}

// execReturn 归还：归还日晚于借期最后一日即逾期，逾期天数一次计入累计；
// 达到阈值进入暂停。归还后封存卷不再分配且取消全部预约，
// 否则自动分配给队首起第一个有效预约者。
func (s *Service) execReturn(op Op) Result {
	v := s.volumes[op.VolumeID]
	if v == nil {
		return Result{Err: newErr(ErrNotFound, -1, "卷 %q 不存在", op.VolumeID)}
	}
	l := v.loan
	if l == nil {
		return Result{Err: newErr(ErrInvalidState, -1, "卷 %q 未借出，无法归还", v.id)}
	}
	b := l.borrower
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
	if v.status == StatusSealed {
		v.queue.clear()
	} else {
		v.status = StatusAvailable
		s.assignHead(v, op.Now)
	}
	return Result{OverdueDays: overdue}
}

// execRenew 续借：窗口为借期最后一日前 RenewWindowDays 天（含最后一日，
// 起点恰等允许）；受次数上限约束；存在有效预约时不得续借；
// 机密及以上需审批人同意且审批人不得是本人；
// 新借期自原借期最后一日的下一日起算。
func (s *Service) execRenew(op Op) Result {
	v := s.volumes[op.VolumeID]
	if v == nil {
		return Result{Err: newErr(ErrNotFound, -1, "卷 %q 不存在", op.VolumeID)}
	}
	b := s.borrowers[op.BorrowerID]
	if b == nil {
		return Result{Err: newErr(ErrNotFound, -1, "借阅人 %q 不存在", op.BorrowerID)}
	}
	l := v.loan
	if l == nil || l.borrower != b {
		return Result{Err: newErr(ErrInvalidState, -1, "卷 %q 未借出给借阅人 %q", v.id, b.id)}
	}
	if s.effSuspended(b, op.Now) {
		return Result{Err: newErr(ErrSuspended, -1, "借阅人 %q 处于暂停状态", b.id)}
	}
	if l.renews >= s.cfg.MaxRenews {
		return Result{Err: newErr(ErrRenew, -1,
			"卷 %q 本次借阅续借次数已达上限 %d", v.id, s.cfg.MaxRenews)}
	}
	windowStart := l.dueDay - (s.cfg.RenewWindowDays - 1)
	if op.Now < windowStart {
		return Result{Err: newErr(ErrRenew, -1,
			"续借窗口未到: 窗口起点为第 %d 日，当前第 %d 日", windowStart, op.Now)}
	}
	if op.Now > l.dueDay {
		return Result{Err: newErr(ErrRenew, -1,
			"已逾借期最后一日 %d，续借窗口已过", l.dueDay)}
	}
	if v.queue.len > 0 {
		return Result{Err: newErr(ErrReservation, -1,
			"卷 %q 存在 %d 个有效预约，不得续借", v.id, v.queue.len)}
	}
	if v.level >= Confidential {
		if op.ApproverID == "" {
			return Result{Err: newErr(ErrInvalidParam, -1,
				"密级%s 的卷续借需审批人", v.level)}
		}
		if op.ApproverID == b.id {
			return Result{Err: newErr(ErrInvalidParam, -1,
				"审批人不得是借阅人本人 %q", b.id)}
		}
		if s.borrowers[op.ApproverID] == nil {
			return Result{Err: newErr(ErrNotFound, -1, "审批人 %q 不存在", op.ApproverID)}
		}
	}
	l.renews++
	l.dueDay = l.dueDay + 1 + s.cfg.LoanDays[v.level]
	return Result{NewDueDay: l.dueDay}
}

// execPickup 取卷：被分配者须在取卷期限内取走，期限最后一日仍可取；
// 取卷时复核密级与暂停状态。取卷当日为借出日。
func (s *Service) execPickup(op Op) Result {
	v := s.volumes[op.VolumeID]
	b := s.borrowers[op.BorrowerID]
	var undo *expiryUndo
	if v != nil {
		undo = s.processExpiry(v, op.Now)
	}
	fail := func(e *Error) Result {
		undoExpiry(undo)
		return Result{Err: e}
	}
	if v == nil {
		return fail(newErr(ErrNotFound, -1, "卷 %q 不存在", op.VolumeID))
	}
	if b == nil {
		return fail(newErr(ErrNotFound, -1, "借阅人 %q 不存在", op.BorrowerID))
	}
	if v.assignment == nil {
		return fail(newErr(ErrInvalidState, -1, "卷 %q 无待取分配", v.id))
	}
	if v.assignment.borrower != b {
		return fail(newErr(ErrInvalidState, -1,
			"卷 %q 已分配给借阅人 %q", v.id, v.assignment.borrower.id))
	}
	if v.status == StatusSealed {
		return fail(newErr(ErrInvalidState, -1, "卷 %q 已封存，不得借出", v.id))
	}
	if b.maxLevel < v.level {
		return fail(newErr(ErrClearance, -1,
			"借阅人 %q 最高密级%s 低于卷 %q 密级%s", b.id, b.maxLevel, v.id, v.level))
	}
	if s.effSuspended(b, op.Now) {
		return fail(newErr(ErrSuspended, -1, "借阅人 %q 处于暂停状态", b.id))
	}
	s.syncBorrower(b, op.Now)
	v.assignment = nil
	due := op.Now + s.cfg.LoanDays[v.level]
	v.status = StatusLent
	v.loan = &loan{borrower: b, borrowDay: op.Now, dueDay: due}
	b.activeLoans++
	return Result{DueDay: due}
}

// Snapshot 返回 now 时刻的全量可观察状态（懒惰推导，不改变任何状态）。
func (s *Service) Snapshot(now int) StateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(now)
}

func (s *Service) snapshotLocked(now int) StateSnapshot {
	snap := StateSnapshot{Watermark: s.watermark, HasClock: s.hasClock}
	vids := make([]string, 0, len(s.volumes))
	for id := range s.volumes {
		vids = append(vids, id)
	}
	sort.Strings(vids)
	for _, id := range vids {
		v := s.volumes[id]
		vs := VolumeSnapshot{ID: v.id, Level: v.level, Status: v.status, Queue: v.queue.ids()}
		if v.loan != nil {
			vs.Loan = &LoanSnapshot{
				BorrowerID: v.loan.borrower.id,
				BorrowDay:  v.loan.borrowDay,
				DueDay:     v.loan.dueDay,
				Renews:     v.loan.renews,
			}
		}
		if v.assignment != nil && now <= v.assignment.deadline {
			vs.Assignment = &AssignmentSnapshot{
				BorrowerID: v.assignment.borrower.id,
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
		snap.Borrowers = append(snap.Borrowers, BorrowerSnapshot{
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

// 以下为便捷方法，均等价于构造相应 Op 调用 Apply。

// AddVolume 登记档案卷。
func (s *Service) AddVolume(now int, id string, level SecretLevel) Result {
	return s.Apply(Op{Kind: OpAddVolume, Now: now, VolumeID: id, Level: level})
}

// AddBorrower 登记借阅人。
func (s *Service) AddBorrower(now int, id string, maxLevel SecretLevel) Result {
	return s.Apply(Op{Kind: OpAddBorrower, Now: now, BorrowerID: id, Level: maxLevel})
}

// SetBorrowerLevel 调整借阅人最高密级。
func (s *Service) SetBorrowerLevel(now int, id string, maxLevel SecretLevel) Result {
	return s.Apply(Op{Kind: OpSetBorrowerLevel, Now: now, BorrowerID: id, Level: maxLevel})
}

// Seal 封存档案卷。
func (s *Service) Seal(now int, volumeID string) Result {
	return s.Apply(Op{Kind: OpSeal, Now: now, VolumeID: volumeID})
}

// Borrow 多卷同借（单卷传一个标识即可）。
func (s *Service) Borrow(now int, borrowerID string, volumeIDs ...string) Result {
	return s.Apply(Op{Kind: OpBorrow, Now: now, BorrowerID: borrowerID, VolumeIDs: volumeIDs})
}

// Reserve 预约借出中的卷。
func (s *Service) Reserve(now int, borrowerID, volumeID string) Result {
	return s.Apply(Op{Kind: OpReserve, Now: now, BorrowerID: borrowerID, VolumeID: volumeID})
}

// Return 归还卷。
func (s *Service) Return(now int, volumeID string) Result {
	return s.Apply(Op{Kind: OpReturn, Now: now, VolumeID: volumeID})
}

// Renew 续借；机密及以上需传入审批人。
func (s *Service) Renew(now int, borrowerID, volumeID, approverID string) Result {
	return s.Apply(Op{Kind: OpRenew, Now: now, BorrowerID: borrowerID, VolumeID: volumeID, ApproverID: approverID})
}

// Pickup 被分配者在取卷期限内取卷。
func (s *Service) Pickup(now int, borrowerID, volumeID string) Result {
	return s.Apply(Op{Kind: OpPickup, Now: now, BorrowerID: borrowerID, VolumeID: volumeID})
}
