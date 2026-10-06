package remittance

// 独立朴素模型：按规格直接实现，不复用被测系统的任何内部结构。
// 额度占用通过对全部历史汇款的全量扫描计算，乘积用 math/big 求值，
// 以此与被测系统的摊还 O(1) 实现互为对照。

import (
	"fmt"
	"math/big"
)

type naiveQuote struct {
	remitter  string
	srcAmount int64
	rate      int64
	createdAt int64
	consumed  bool
}

type naiveRem struct {
	id        string
	remitter  string
	payee     string
	quoteID   string
	target    int64
	hold      int64
	day       int64
	submitNow int64
	deadline  int64
	status    Status // 仅记录被操作显式改变的状态；逾期由 statusAt 按需推导
}

type naiveIdem struct {
	remitter string
	quoteID  string
	payee    string
	result   SubmitResult
}

type naiveModel struct {
	cfg        Config
	lastNow    int64
	quoteSeq   int64
	remSeq     int64
	quotes     map[string]*naiveQuote
	rems       []*naiveRem
	byID       map[string]*naiveRem
	idem       map[string]naiveIdem
	sanctioned map[string]bool
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:        cfg,
		lastNow:    -1,
		quotes:     make(map[string]*naiveQuote),
		byID:       make(map[string]*naiveRem),
		idem:       make(map[string]naiveIdem),
		sanctioned: make(map[string]bool),
	}
}

// statusAt 计算汇款在 now 的逻辑状态：待审核且 now 超过截止时刻即已逾期失败。
func (m *naiveModel) statusAt(r *naiveRem, now int64) Status {
	if r.status == StatusPendingReview && now > r.deadline {
		return StatusFailed
	}
	return r.status
}

// holdsAt 全量扫描，计算 remitter 在 now 的当日与滚动年度占用。
func (m *naiveModel) holdsAt(remitter string, now int64) (dayUsed, rollingUsed int64) {
	curDay := now / 86400
	for _, r := range m.rems {
		if r.remitter != remitter || m.statusAt(r, now) == StatusFailed {
			continue
		}
		if r.day == curDay {
			dayUsed += r.hold
		}
		if curDay-r.day < 365 {
			rollingUsed += r.hold
		}
	}
	return dayUsed, rollingUsed
}

func (m *naiveModel) checkClock(now int64) error {
	if now < m.lastNow {
		return ErrClockRegression
	}
	return nil
}

func (m *naiveModel) RequestQuote(remitter string, srcAmount, rate, now int64) (string, error, string) {
	if remitter == "" || srcAmount <= 0 || rate <= 0 || now < 0 {
		return "", ErrInvalidParams, "参数非法"
	}
	if err := m.checkClock(now); err != nil {
		return "", err, "时钟回退"
	}
	m.quoteSeq++
	id := fmt.Sprintf("Q-%d", m.quoteSeq)
	m.quotes[id] = &naiveQuote{remitter: remitter, srcAmount: srcAmount, rate: rate, createdAt: now}
	m.lastNow = now
	return id, nil, "报价已创建"
}

func (m *naiveModel) Submit(remitter, idemKey, quoteID, payee string, now int64) (SubmitResult, error, string) {
	if remitter == "" || idemKey == "" || quoteID == "" || payee == "" || now < 0 {
		return SubmitResult{}, ErrInvalidParams, "参数非法"
	}
	if err := m.checkClock(now); err != nil {
		return SubmitResult{}, err, "时钟回退"
	}
	if m.sanctioned[payee] {
		return SubmitResult{}, ErrSanctioned, "收款人命中制裁"
	}
	if e, ok := m.idem[idemKey]; ok {
		if e.remitter == remitter && e.quoteID == quoteID && e.payee == payee {
			m.lastNow = now
			return e.result, nil, "幂等重放，返回原结果"
		}
		return SubmitResult{}, ErrIdemConflict, "幂等键相同但参数不同"
	}
	q, ok := m.quotes[quoteID]
	if !ok || q.remitter != remitter {
		return SubmitResult{}, ErrQuoteNotFound, "报价不存在"
	}
	if q.consumed {
		return SubmitResult{}, ErrQuoteConsumed, "报价已消耗"
	}
	if now > q.createdAt+m.cfg.QuoteTTL && q.createdAt+m.cfg.QuoteTTL >= q.createdAt {
		return SubmitResult{}, ErrQuoteExpired, "报价已过期"
	}
	// 用 big.Int 独立计算目标额（向下取整）与占用额（向上取整）。
	prod := new(big.Int).Mul(big.NewInt(q.srcAmount), big.NewInt(q.rate))
	qq, rr := new(big.Int).QuoRem(prod, big.NewInt(1_000_000), new(big.Int))
	if !qq.IsInt64() {
		return SubmitResult{}, ErrSingleLimit, "乘积超出表示范围，必超单笔限额"
	}
	target := qq.Int64()
	hold := target
	if rr.Sign() > 0 {
		hold = target + 1
		if hold < target {
			return SubmitResult{}, ErrSingleLimit, "占用额溢出，必超单笔限额"
		}
	}
	if hold > m.cfg.SingleLimit {
		return SubmitResult{}, ErrSingleLimit, "超单笔限额"
	}
	dayUsed, rollingUsed := m.holdsAt(remitter, now)
	if hold > m.cfg.DayLimit-dayUsed {
		return SubmitResult{}, ErrDayLimit, "超日限额"
	}
	if hold > m.cfg.YearLimit-rollingUsed {
		return SubmitResult{}, ErrYearLimit, "超年度额度"
	}
	q.consumed = true
	m.remSeq++
	id := fmt.Sprintf("R-%d", m.remSeq)
	status := StatusSucceeded
	deadline := now + m.cfg.ReviewTimeout
	if deadline < now {
		deadline = 1<<63 - 1
	}
	if target >= m.cfg.ReviewThreshold {
		status = StatusPendingReview
	}
	m.rems = append(m.rems, &naiveRem{
		id: id, remitter: remitter, payee: payee, quoteID: quoteID,
		target: target, hold: hold, day: now / 86400,
		submitNow: now, deadline: deadline, status: status,
	})
	m.byID[id] = m.rems[len(m.rems)-1]
	res := SubmitResult{RemittanceID: id, TargetAmount: target, HoldAmount: hold, Status: status}
	m.idem[idemKey] = naiveIdem{remitter: remitter, quoteID: quoteID, payee: payee, result: res}
	m.lastNow = now
	if status == StatusPendingReview {
		return res, nil, "接受：进入待审核"
	}
	return res, nil, "接受：直接出款"
}

func (m *naiveModel) review(id string, now int64, approve bool, opName string) (error, string) {
	if id == "" || now < 0 {
		return ErrInvalidParams, "参数非法"
	}
	if err := m.checkClock(now); err != nil {
		return err, "时钟回退"
	}
	r, ok := m.byID[id]
	if !ok {
		return ErrNotFound, "汇款不存在"
	}
	if m.statusAt(r, now) != StatusPendingReview {
		return ErrInvalidState, "当前状态不允许（含已逾期失败）"
	}
	if approve {
		r.status = StatusSucceeded
	} else {
		r.status = StatusFailed
	}
	m.lastNow = now
	return nil, opName + "成功"
}

func (m *naiveModel) Approve(id string, now int64) (error, string) {
	return m.review(id, now, true, "批准")
}

func (m *naiveModel) Reject(id string, now int64) (error, string) {
	return m.review(id, now, false, "拒绝")
}

func (m *naiveModel) Withdraw(id string, now int64) (error, string) {
	return m.review(id, now, false, "撤回")
}

func (m *naiveModel) GetRemittance(id string, now int64) (RemittanceView, error, string) {
	if id == "" || now < 0 {
		return RemittanceView{}, ErrInvalidParams, "参数非法"
	}
	if err := m.checkClock(now); err != nil {
		return RemittanceView{}, err, "时钟回退"
	}
	r, ok := m.byID[id]
	if !ok {
		return RemittanceView{}, ErrNotFound, "汇款不存在"
	}
	m.lastNow = now
	return RemittanceView{
		ID: r.id, Remitter: r.remitter, Payee: r.payee, QuoteID: r.quoteID,
		TargetAmount: r.target, HoldAmount: r.hold, DayIndex: r.day,
		SubmitNow: r.submitNow, Deadline: r.deadline, Status: m.statusAt(r, now),
	}, nil, "查询成功"
}

func (m *naiveModel) QueryUsage(remitter string, now int64) (Usage, error, string) {
	if remitter == "" || now < 0 {
		return Usage{}, ErrInvalidParams, "参数非法"
	}
	if err := m.checkClock(now); err != nil {
		return Usage{}, err, "时钟回退"
	}
	dayUsed, rollingUsed := m.holdsAt(remitter, now)
	m.lastNow = now
	return Usage{DayIndex: now / 86400, DayUsed: dayUsed, RollingYearUsed: rollingUsed}, nil, "查询成功"
}

func (m *naiveModel) AddSanctionedPayee(payee string)    { m.sanctioned[payee] = true }
func (m *naiveModel) RemoveSanctionedPayee(payee string) { delete(m.sanctioned, payee) }
