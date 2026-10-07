package ledger

import "sync"

// Stats 记录内部堆操作次数，用于以可验证（非计时）的方式证明
// 选批与锁定判定的开销不随历史数据总量增长。
type Stats struct {
	BatchHeapOps int // 批次堆 push/pop/remove 总次数
	SlipHeapOps  int // 单据堆 push/remove 总次数
}

// System 是麻醉与精神类药品专用账册。
// 所有导出方法都可并发调用，内部以互斥锁串行化，
// 结果等价于某个串行顺序；相同操作序列重放得到完全相同的结果。
type System struct {
	mu    sync.Mutex
	clk   clock
	auth  *authRegistry
	drugs map[string]*drugStock
	depts map[string]*deptState
	slips map[string]*slip
	stats Stats
}

func NewSystem() *System {
	return &System{
		auth:  newAuthRegistry(),
		drugs: make(map[string]*drugStock),
		depts: make(map[string]*deptState),
		slips: make(map[string]*slip),
	}
}

func validMoment(t int64) bool { return t >= minNow && t <= maxNow }

func validQty(q int) bool { return q >= 1 && q <= 1_000_000 }

// 结清中的已用量、退回量、残液量允许为 0。
func validAmount(a int) bool { return a >= 0 && a <= 1_000_000 }

func nonEmpty(ss ...string) bool {
	for _, s := range ss {
		if s == "" {
			return false
		}
	}
	return true
}

// checkReviewers 校验复核人人数、互不相同、且不是申请人本人。
func checkReviewers(reviewers []string, applicant string, hasApplicant bool) *OpError {
	if len(reviewers) != 2 {
		return errf(ErrReviewer, "须两名复核人共同确认，实际 %d 人", len(reviewers))
	}
	if reviewers[0] == reviewers[1] {
		return errf(ErrReviewer, "两名复核人为同一人 %q", reviewers[0])
	}
	if hasApplicant && (reviewers[0] == applicant || reviewers[1] == applicant) {
		return errf(ErrReviewer, "复核人不得是申请人本人 %q", applicant)
	}
	return nil
}

func (s *System) checkAuth(reviewers []string, now int64) *OpError {
	for _, r := range reviewers {
		if !s.auth.validAt(r, now) {
			return errf(ErrAuth, "复核人 %q 在 now=%d 授权无效", r, now)
		}
	}
	return nil
}

func (s *System) dept(name string) *deptState {
	d, ok := s.depts[name]
	if !ok {
		d = newDeptState(&s.stats)
		s.depts[name] = d
	}
	return d
}

// Stats 返回内部统计快照。
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Now 返回当前账册时钟（最后一次被接受操作的 now）。
func (s *System) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clk.now()
}

// DeptLocked 查询科室在 now 时刻是否被锁定。
// 只读、O(1)：只看差额计数与逾期堆堆顶，与历史单据总数无关。
func (s *System) DeptLocked(now int64, dept string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.depts[dept]
	return d != nil && d.locked(now)
}

// GrantAuth 登记人员的授权窗口 [from, until)，左闭右开；重复登记覆盖旧记录。
func (s *System) GrantAuth(now int64, person string, from, until int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !validMoment(from) || !validMoment(until) || from >= until || !nonEmpty(person) {
		return errf(ErrInvalidParam, "授权参数非法: person=%q from=%d until=%d now=%d", person, from, until, now)
	}
	if e := s.clk.check(now); e != nil {
		return e
	}
	s.auth.grant(person, from, until)
	s.clk.advance(now)
	return nil
}

// RevokeAuth 在 now 时刻撤销人员授权，撤销时刻起即失效。
func (s *System) RevokeAuth(now int64, person string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !nonEmpty(person) {
		return errf(ErrInvalidParam, "撤销参数非法: person=%q now=%d", person, now)
	}
	if e := s.clk.check(now); e != nil {
		return e
	}
	if !s.auth.has(person) {
		return errf(ErrNotFound, "人员 %q 无授权记录", person)
	}
	s.auth.revoke(person, now)
	s.clk.advance(now)
	return nil
}

// Inbound 入库登记：同一药品下批号不得重复；效期恰等于 now 视为已过期。
func (s *System) Inbound(now int64, drug, batchID string, qty int, expiry int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !validMoment(expiry) || !validQty(qty) || !nonEmpty(drug, batchID) {
		return errf(ErrInvalidParam, "入库参数非法: drug=%q batch=%q qty=%d expiry=%d now=%d", drug, batchID, qty, expiry, now)
	}
	if e := s.clk.check(now); e != nil {
		return e
	}
	d, ok := s.drugs[drug]
	if !ok {
		d = newDrugStock(&s.stats)
		s.drugs[drug] = d
	}
	if _, dup := d.batches[batchID]; dup {
		return errf(ErrState, "药品 %q 下批号 %q 已存在", drug, batchID)
	}
	d.addInbound(batchID, qty, expiry, now)
	s.clk.advance(now)
	return nil
}

// Withdraw 领用：按效期最早的可发批次优先，效期相同按入库先后，
// 可跨批次，全有或全无。返回每个批次分出的数量。
func (s *System) Withdraw(now int64, slipID, dept, applicant, drug string, qty int, reviewers []string) ([]BatchLine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !validQty(qty) || !nonEmpty(slipID, dept, applicant, drug) || !nonEmpty(reviewers...) {
		return nil, errf(ErrInvalidParam, "领用参数非法: slip=%q dept=%q applicant=%q drug=%q qty=%d now=%d",
			slipID, dept, applicant, drug, qty, now)
	}
	if e := s.clk.check(now); e != nil {
		return nil, e
	}
	if e := checkReviewers(reviewers, applicant, true); e != nil {
		return nil, e
	}
	if e := s.checkAuth(reviewers, now); e != nil {
		return nil, e
	}
	d, ok := s.drugs[drug]
	if !ok {
		return nil, errf(ErrNotFound, "药品 %q 不存在", drug)
	}
	if _, dup := s.slips[slipID]; dup {
		return nil, errf(ErrState, "单据 %q 已存在", slipID)
	}
	dp := s.depts[dept]
	if dp != nil && dp.locked(now) {
		return nil, errf(ErrDeptLocked, "科室 %q 存在逾期未结清或差额待处理单据", dept)
	}
	if dp != nil && dp.openCount() >= maxOpenSlips {
		return nil, errf(ErrSlipLimit, "科室 %q 未结清单据已达 %d 张上限", dept, maxOpenSlips)
	}
	// 非变异预检：被拒绝的操作不得改变任何状态。
	if d.availableAt(now) < qty {
		return nil, errf(ErrStock, "药品 %q 可用库存 %d 不足 %d", drug, d.availableAt(now), qty)
	}
	d.migrate(now)
	lines := make([]BatchLine, 0, 2)
	remaining := qty
	for remaining > 0 {
		b := d.active[0]
		take := b.qty
		if take > remaining {
			take = remaining
		}
		b.qty -= take
		remaining -= take
		lines = append(lines, BatchLine{BatchID: b.id, Qty: take})
		if b.qty == 0 {
			d.popTop()
		}
	}
	d.bookTotal -= qty
	d.totalIssued += qty
	sl := &slip{
		id: slipID, dept: dept, applicant: applicant, drug: drug,
		lines: lines, qty: qty, issueNow: now, deadline: now + settleWindow,
		status: SlipOpen, heapIndex: -1,
	}
	s.slips[slipID] = sl
	dp = s.dept(dept)
	dp.addOpen(sl)
	s.clk.advance(now)
	return lines, nil
}

// Settle 结清：used+returned+residual 等于领出量则结清成功；
// 小于则转差额待处理；大于则拒绝且不改变单据。逾期单据仍可补结清。
func (s *System) Settle(now int64, slipID string, used, returned, residual int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !validAmount(used) || !validAmount(returned) || !validAmount(residual) || !nonEmpty(slipID) {
		return errf(ErrInvalidParam, "结清参数非法: slip=%q used=%d returned=%d residual=%d now=%d",
			slipID, used, returned, residual, now)
	}
	if e := s.clk.check(now); e != nil {
		return e
	}
	sl, ok := s.slips[slipID]
	if !ok {
		return errf(ErrNotFound, "单据 %q 不存在", slipID)
	}
	if sl.status != SlipOpen {
		return errf(ErrState, "单据 %q 当前状态为 %s，不能结清", slipID, sl.status)
	}
	sum := used + returned + residual
	if sum > sl.qty {
		return errf(ErrQuantity, "结清合计 %d 超出领出量 %d", sum, sl.qty)
	}
	d := s.drugs[sl.drug]
	// 退回量按领用时的批次顺序退回原批次账面（即使该批次此时已过期）。
	rest := returned
	for _, ln := range sl.lines {
		if rest == 0 {
			break
		}
		back := ln.Qty
		if back > rest {
			back = rest
		}
		b := d.batches[ln.BatchID]
		d.returnTo(b, back)
		rest -= back
	}
	dp := s.depts[sl.dept]
	dp.removeOpen(sl)
	sl.used, sl.returned, sl.residual = used, returned, residual
	if sum == sl.qty {
		sl.status = SlipSettled
	} else {
		sl.status = SlipDiscrepancy
		sl.diff = sl.qty - sum
		dp.discrepancy++
	}
	s.clk.advance(now)
	return nil
}

// ResolveDiscrepancy 差额处理：须两名复核人确认，处理后单据视为已结清，
// 不改变库存。
func (s *System) ResolveDiscrepancy(now int64, slipID string, reviewers []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !nonEmpty(slipID) || !nonEmpty(reviewers...) {
		return errf(ErrInvalidParam, "差额处理参数非法: slip=%q now=%d", slipID, now)
	}
	if e := s.clk.check(now); e != nil {
		return e
	}
	if e := checkReviewers(reviewers, "", false); e != nil {
		return e
	}
	if e := s.checkAuth(reviewers, now); e != nil {
		return e
	}
	sl, ok := s.slips[slipID]
	if !ok {
		return errf(ErrNotFound, "单据 %q 不存在", slipID)
	}
	if sl.status != SlipDiscrepancy {
		return errf(ErrState, "单据 %q 当前状态为 %s，非差额待处理", slipID, sl.status)
	}
	sl.status = SlipSettled
	s.depts[sl.dept].discrepancy--
	s.clk.advance(now)
	return nil
}

// Destroy 销毁某批次的账面余量，数量不得超过当前账面余量，
// 须两名复核人见证；销毁后账面相应减少且不可撤回；未过期批次也允许销毁。
func (s *System) Destroy(now int64, drug, batchID string, qty int, reviewers []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validMoment(now) || !validQty(qty) || !nonEmpty(drug, batchID) || !nonEmpty(reviewers...) {
		return errf(ErrInvalidParam, "销毁参数非法: drug=%q batch=%q qty=%d now=%d", drug, batchID, qty, now)
	}
	if e := s.clk.check(now); e != nil {
		return e
	}
	if e := checkReviewers(reviewers, "", false); e != nil {
		return e
	}
	if e := s.checkAuth(reviewers, now); e != nil {
		return e
	}
	d, ok := s.drugs[drug]
	if !ok {
		return errf(ErrNotFound, "药品 %q 不存在", drug)
	}
	b, ok := d.batches[batchID]
	if !ok {
		return errf(ErrNotFound, "药品 %q 下批号 %q 不存在", drug, batchID)
	}
	if qty > b.qty {
		return errf(ErrQuantity, "销毁数量 %d 超出批次 %q 账面余量 %d", qty, batchID, b.qty)
	}
	d.destroy(b, qty)
	s.clk.advance(now)
	return nil
}
