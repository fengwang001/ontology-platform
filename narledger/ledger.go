package narledger

import (
	"fmt"
	"io"
	"sync"
)

// Ledger 麻醉与精神类药品专用账册。所有方法并发安全，
// 结果等价于某个串行顺序。
type Ledger struct {
	mu     sync.Mutex
	clk    clock
	auth   *authBook
	stock  *stockBook
	orders *orderBook
	depts  *deptBook
	logger *StepLogger

	// 复杂度可验证性：记录最近一次领用在 FEFO 选批中触及的批次节点数。
	dispenseTouched int64
}

// New 创建空账册。
func New() *Ledger {
	return &Ledger{
		auth:   newAuthBook(),
		stock:  newStockBook(),
		orders: newOrderBook(),
		depts:  newDeptBook(),
	}
}

// WithLogger 将逐步日志（输入/输出/判定依据）写入 w。
func (l *Ledger) WithLogger(w io.Writer) *Ledger {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger = newStepLogger(w)
	return l
}

func (l *Ledger) fail(op string, e *OpError) error {
	l.logger.logf("OP=%s RESULT=REJECT CODE=%s REASON=%s", op, e.Code, e.Reason)
	return e
}

func (l *Ledger) acceptf(op, format string, args ...any) {
	l.logger.logf("OP=%s RESULT=ACCEPT %s", op, fmt.Sprintf(format, args...))
}

// RegisterGrant 登记一条 [start, end) 授权；start 作为该操作的时刻。
func (l *Ledger) RegisterGrant(person string, start, end int64) error {
	const op = "RegisterGrant"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmpty(op, "person", person); err != nil {
		return l.fail(op, err)
	}
	if start < 0 || start > 1e9 || end < 0 || end > 1e9 || start >= end {
		return l.fail(op, opError(op, ErrInvalidParam,
			"授权区间非法: start=%d end=%d（需 0<=start<end<=10^9）", start, end))
	}
	if err := l.clk.check(op, start); err != nil {
		return l.fail(op, err)
	}
	l.auth.add(person, start, end)
	l.clk.advance(start)
	l.acceptf(op, "person=%s interval=[%d,%d)", person, start, end)
	return nil
}

// RevokeGrant 自 at 起撤销某人全部授权。
func (l *Ledger) RevokeGrant(person string, at int64) error {
	const op = "RevokeGrant"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmpty(op, "person", person); err != nil {
		return l.fail(op, err)
	}
	if err := validNow(op, at); err != nil {
		return l.fail(op, err)
	}
	if err := l.clk.check(op, at); err != nil {
		return l.fail(op, err)
	}
	if err := l.auth.revoke(person, at); err != nil {
		err.Op = op
		return l.fail(op, err)
	}
	l.clk.advance(at)
	l.acceptf(op, "person=%s revokeAt=%d", person, at)
	return nil
}

// Receive 入库登记。允许登记入库时已过期的批次（账面保留、不可发）。
func (l *Ledger) Receive(now int64, drug, lot string, qty, expireAt int64) error {
	const op = "Receive"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmptyMany(op, map[string]string{"drug": drug, "lot": lot}); err != nil {
		return l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := positiveQty(op, qty); err != nil {
		return l.fail(op, err)
	}
	if expireAt < 0 || expireAt > 1e9 {
		return l.fail(op, opError(op, ErrInvalidParam, "expireAt=%d 越界", expireAt))
	}
	if err := l.clk.check(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := l.stock.register(op, drug, lot, qty, expireAt, now); err != nil {
		return l.fail(op, err)
	}
	l.clk.advance(now)
	l.acceptf(op, "drug=%s lot=%s qty=%d expireAt=%d issuable=%v",
		drug, lot, qty, expireAt, now < expireAt)
	return nil
}

// Dispense 双人复核领用，按 FEFO 跨批次原子分出（全有或全无）。
func (l *Ledger) Dispense(now int64, id, dept, applicant, drug string, qty int64, r1, r2 string) error {
	const op = "Dispense"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmptyMany(op, map[string]string{
		"id": id, "dept": dept, "applicant": applicant, "drug": drug,
		"reviewer1": r1, "reviewer2": r2,
	}); err != nil {
		return l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := positiveQty(op, qty); err != nil {
		return l.fail(op, err)
	}
	if err := l.clk.check(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := l.checkReviewers(op, now, applicant, r1, r2); err != nil {
		return l.fail(op, err)
	}
	h := l.stock.heapFor(drug)
	if h == nil {
		return l.fail(op, opError(op, ErrNotFound, "药品 %s 从未入库", drug))
	}
	d := l.depts.get(dept)
	d.refresh(now, func(oid string) bool {
		oo := l.orders.get(oid)
		return oo != nil && oo.status == orderOpen
	})
	if d.locked() {
		return l.fail(op, opError(op, ErrDeptLocked,
			"科室 %s 存在逾期未结清或差额待处理单据 (overdue=%d discrepancy=%d)",
			dept, len(d.overdue), len(d.discrepancy)))
	}
	if d.openCount >= 3 {
		return l.fail(op, opError(op, ErrOpenLimit,
			"科室 %s 已有 %d 张未结清单据（上限 3）", dept, d.openCount))
	}

	// FEFO 选批：堆顶过期即永久移出可发索引（账面仍保留待销毁）；
	// 选批期间任何弹出都先记账，只有在最终接受时才生效；
	// 库存不足时全部回插，保证被拒绝操作不改变任何状态（含索引）。
	l.dispenseTouched = 0
	type take struct {
		b   *batch
		qty int64
	}
	var plan []take
	var popped []*batch
	remaining := qty
	for remaining > 0 {
		b := h.Peek()
		if b == nil {
			break
		}
		l.dispenseTouched++
		if b.expireAt <= now {
			popped = append(popped, h.PopB()) // now==expireAt 即视为过期
			continue
		}
		give := b.qty
		if give > remaining {
			give = remaining
		}
		plan = append(plan, take{b: b, qty: give})
		remaining -= give
		if b.qty-give == 0 {
			popped = append(popped, h.PopB())
		}
	}
	if remaining > 0 {
		// 全有或全无：尚未改动任何账面；把选批时弹出的索引节点全部恢复。
		for _, b := range popped {
			h.PushB(b)
		}
		return l.fail(op, opError(op, ErrStock,
			"药品 %s 可发库存不足 %d，尚缺 %d（选批触及节点 %d）",
			drug, qty, remaining, l.dispenseTouched))
	}

	o := &order{
		id:        id,
		dept:      dept,
		applicant: applicant,
		drug:      drug,
		qty:       qty,
		createdAt: now,
		deadline:  now + 24*3600,
		status:    orderOpen,
	}
	for _, t := range plan {
		t.b.qty -= t.qty
		o.allocs = append(o.allocs, allocation{drug: drug, lot: t.b.lot, qty: t.qty})
	}
	l.orders.add(o)
	d.noteOpen(id, o.deadline)
	l.clk.advance(now)
	l.acceptf(op, "id=%s dept=%s drug=%s qty=%d allocs=%v reviewers=%s,%s deadline=%d",
		id, dept, drug, qty, o.allocs, r1, r2, o.deadline)
	return nil
}

// Settle 结清：used+returned+residue 等于领出量结清，小于转差额，大于拒绝。
func (l *Ledger) Settle(now int64, id string, used, returned, residue int64) error {
	const op = "Settle"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmpty(op, "id", id); err != nil {
		return l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := settleQty(op, used, returned, residue); err != nil {
		return l.fail(op, err)
	}
	if err := l.clk.check(op, now); err != nil {
		return l.fail(op, err)
	}
	o := l.orders.get(id)
	if o == nil {
		return l.fail(op, opError(op, ErrNotFound, "单据 %s 不存在", id))
	}
	if o.status != orderOpen {
		return l.fail(op, opError(op, ErrState,
			"单据 %s 状态为 %s，不能结清", id, o.status))
	}
	total := used + returned + residue
	if total > o.qty {
		return l.fail(op, opError(op, ErrExceed,
			"结清三项之和 %d 大于领出量 %d，单据不变", total, o.qty))
	}
	// 退回量按 FEFO 分出顺序逐批回补原批次（即使该批次此刻已过期）。
	rest := returned
	for i := range o.allocs {
		a := &o.allocs[i]
		give := rest
		if give > a.qty {
			give = a.qty
		}
		if give > 0 {
			b := l.stock.batch(a.drug, a.lot)
			b.qty += give
			l.stock.syncHeap(b, now)
			rest -= give
		}
		if rest == 0 {
			break
		}
	}
	o.used, o.returned, o.residue = used, returned, residue
	dp := l.depts.get(o.dept)
	dp.refresh(now, func(oid string) bool {
		oo := l.orders.get(oid)
		return oo != nil && oo.status == orderOpen
	})
	if total < o.qty {
		o.status = orderDiscrepancy
		o.shortfall = o.qty - total
		dp.settleOpen(id, true)
		l.clk.advance(now)
		l.acceptf(op, "id=%s -> DISCREPANCY shortfall=%d used=%d returned=%d residue=%d overdue=%v",
			id, o.shortfall, used, returned, residue, now > o.deadline)
		return nil
	}
	o.status = orderSettled
	dp.settleOpen(id, false)
	l.clk.advance(now)
	l.acceptf(op, "id=%s -> SETTLED used=%d returned=%d residue=%d overdueLate=%v",
		id, used, returned, residue, now > o.deadline)
	return nil
}

// ResolveDiscrepancy 两名复核人确认处理差额，处理后视为已结清，不动库存。
func (l *Ledger) ResolveDiscrepancy(now int64, id, r1, r2 string) error {
	const op = "ResolveDiscrepancy"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmptyMany(op, map[string]string{
		"id": id, "reviewer1": r1, "reviewer2": r2,
	}); err != nil {
		return l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := l.clk.check(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := l.checkReviewers(op, now, "", r1, r2); err != nil {
		return l.fail(op, err)
	}
	o := l.orders.get(id)
	if o == nil {
		return l.fail(op, opError(op, ErrNotFound, "单据 %s 不存在", id))
	}
	if o.status != orderDiscrepancy {
		return l.fail(op, opError(op, ErrState,
			"单据 %s 状态为 %s，非差额待处理", id, o.status))
	}
	o.status = orderSettled
	l.depts.get(o.dept).resolveDiscrepancy(id)
	l.clk.advance(now)
	l.acceptf(op, "id=%s -> SETTLED shortfall=%d 库存不变 reviewers=%s,%s", id, o.shortfall, r1, r2)
	return nil
}

// Destroy 销毁批次账面余量，两名复核人见证；未过期批次也允许销毁。
func (l *Ledger) Destroy(now int64, drug, lot string, qty int64, r1, r2 string) error {
	const op = "Destroy"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmptyMany(op, map[string]string{
		"drug": drug, "lot": lot, "reviewer1": r1, "reviewer2": r2,
	}); err != nil {
		return l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := positiveQty(op, qty); err != nil {
		return l.fail(op, err)
	}
	if err := l.clk.check(op, now); err != nil {
		return l.fail(op, err)
	}
	if err := l.checkReviewers(op, now, "", r1, r2); err != nil {
		return l.fail(op, err)
	}
	b := l.stock.batch(drug, lot)
	if b == nil {
		return l.fail(op, opError(op, ErrNotFound, "批次 %s/%s 不存在", drug, lot))
	}
	if qty > b.qty {
		return l.fail(op, opError(op, ErrExceed,
			"销毁量 %d 超过批次 %s/%s 当前账面余量 %d", qty, drug, lot, b.qty))
	}
	b.qty -= qty
	l.stock.destroyed[drug] += qty
	l.stock.syncHeap(b, now)
	l.clk.advance(now)
	l.acceptf(op, "batch=%s/%s qty=%d remain=%d reviewers=%s,%s", drug, lot, qty, b.qty, r1, r2)
	return nil
}

// DeptLocked 判定科室在 now 是否被锁定。只读，不推进时钟，
// 但 now 早于已接受时刻时报时钟回退。
func (l *Ledger) DeptLocked(now int64, dept string) (bool, error) {
	const op = "DeptLocked"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmpty(op, "dept", dept); err != nil {
		return false, l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return false, l.fail(op, err)
	}
	if err := l.clk.check(op, now); err != nil {
		return false, l.fail(op, err)
	}
	d := l.depts.depts[dept]
	if d == nil {
		return false, nil
	}
	d.refresh(now, func(oid string) bool {
		o := l.orders.get(oid)
		return o != nil && o.status == orderOpen
	})
	locked := d.locked()
	l.logger.logf("OP=%s RESULT=QUERY dept=%s now=%d locked=%v overdue=%d discrepancy=%d",
		op, dept, now, locked, len(d.overdue), len(d.discrepancy))
	return locked, nil
}

// BatchBalance 查询批次当前账面余量（过期批次账面仍保留）。
func (l *Ledger) BatchBalance(now int64, drug, lot string) (int64, error) {
	const op = "BatchBalance"
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := nonEmptyMany(op, map[string]string{"drug": drug, "lot": lot}); err != nil {
		return 0, l.fail(op, err)
	}
	if err := validNow(op, now); err != nil {
		return 0, l.fail(op, err)
	}
	if err := l.clk.check(op, now); err != nil {
		return 0, l.fail(op, err)
	}
	b := l.stock.batch(drug, lot)
	if b == nil {
		return 0, l.fail(op, opError(op, ErrNotFound, "批次 %s/%s 不存在", drug, lot))
	}
	l.logger.logf("OP=%s RESULT=QUERY batch=%s/%s now=%d balance=%d", op, drug, lot, now, b.qty)
	return b.qty, nil
}

// DispenseTouched 返回最近一次领用 FEFO 选批触及的批次节点数（复杂度可验证）。
func (l *Ledger) DispenseTouched() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dispenseTouched
}
