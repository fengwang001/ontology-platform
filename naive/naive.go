// Package naive 是拒付系统的独立朴素模型，仅用于测试对照。
//
// 与引擎的事件堆/增量物化实现完全解耦：不维护任何随时间更新的
// 资金缓存，每次操作与查询都基于案件事件记录按规则全量重算。
// 若引擎与朴素模型在任意操作序列下结果一致，则证明物化优化
// 没有改变任何可观察行为。
package naive

import (
	"sort"

	"ontology/chargeback"
)

// Model 是朴素模型。只保存交易与案件的事件记录及时钟。
type Model struct {
	cfg chargeback.Config

	lastNow      int
	clockStarted bool

	txns  map[string]chargeback.Transaction
	cases map[string]*chargeback.Case
}

// New 创建朴素模型。
func New(cfg chargeback.Config) *Model {
	return &Model{
		cfg:   cfg,
		txns:  make(map[string]chargeback.Transaction),
		cases: make(map[string]*chargeback.Case),
	}
}

func (m *Model) checkClock(now int) *chargeback.Error {
	if m.clockStarted && now < m.lastNow {
		return &chargeback.Error{Code: chargeback.ErrClockRegression}
	}
	return nil
}

func (m *Model) advance(now int) {
	m.lastNow = now
	m.clockStarted = true
}

func (m *Model) state(c *chargeback.Case, now int) chargeback.State {
	return c.State(now, m.cfg)
}

// AddTransaction 与引擎语义一致，全量校验。
func (m *Model) AddTransaction(now int, txn chargeback.Transaction) error {
	if txn.ID == "" || txn.CardID == "" || txn.MerchantID == "" || txn.Amount <= 0 {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	if now < txn.SettleDay {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	if _, dup := m.txns[txn.ID]; dup {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.txns[txn.ID] = txn
	m.advance(now)
	return nil
}

// disputable 全量扫描案件，重算交易可拒付余额。
func (m *Model) disputable(now int, txnID string) int64 {
	held := int64(0)
	for _, c := range m.cases {
		if c.TxnID == txnID && m.state(c, now) != chargeback.StateClosedMerchantWin {
			held += c.Amount
		}
	}
	return m.txns[txnID].Amount - held
}

// basisOccupied 全量扫描案件，判断依据交易是否被占用。
func (m *Model) basisOccupied(now int, basisTxnID string) bool {
	for _, c := range m.cases {
		if c.BasisTxnID == basisTxnID && m.state(c, now) != chargeback.StateClosedMerchantWin {
			return true
		}
	}
	return false
}

// findBasis 与引擎相同的确定性选择规则，但基于全量扫描实现。
func (m *Model) findBasis(now int, txn chargeback.Transaction) string {
	var cands []string
	for id, cand := range m.txns {
		if id == txn.ID || cand.CardID != txn.CardID ||
			cand.MerchantID != txn.MerchantID || cand.Amount != txn.Amount {
			continue
		}
		diff := txn.SettleDay - cand.SettleDay
		if diff <= 0 || diff > m.cfg.DuplicateMatchDays {
			continue
		}
		if m.basisOccupied(now, id) {
			continue
		}
		cands = append(cands, id)
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Strings(cands)
	best := cands[0]
	for _, id := range cands[1:] {
		if m.txns[id].SettleDay > m.txns[best].SettleDay {
			best = id
		}
	}
	return best
}

// OpenDispute 与引擎语义一致，按相同优先级逐项校验。
func (m *Model) OpenDispute(now int, caseID, txnID string, reason chargeback.Reason, amount int64) error {
	if caseID == "" || txnID == "" || !reason.Valid() || amount <= 0 {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	if _, dup := m.cases[caseID]; dup {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	txn, ok := m.txns[txnID]
	if !ok {
		return &chargeback.Error{Code: chargeback.ErrTransactionNotFound}
	}
	if now-txn.SettleDay > m.cfg.WindowDays(reason) {
		return &chargeback.Error{Code: chargeback.ErrWindowExpired}
	}
	for _, c := range m.cases {
		if c.TxnID == txnID && c.Reason == reason &&
			m.state(c, now) == chargeback.StateClosedMerchantWin {
			return &chargeback.Error{Code: chargeback.ErrDuplicateCase}
		}
	}
	if amount > m.disputable(now, txnID) {
		return &chargeback.Error{Code: chargeback.ErrExceedsDisputable}
	}
	basis := ""
	if reason == chargeback.ReasonDuplicate {
		basis = m.findBasis(now, txn)
		if basis == "" {
			return &chargeback.Error{Code: chargeback.ErrNoBasis}
		}
	}
	m.cases[caseID] = &chargeback.Case{
		ID:         caseID,
		TxnID:      txnID,
		MerchantID: txn.MerchantID,
		Reason:     reason,
		Amount:     amount,
		OpenDay:    now,
		RespondDay: -1,
		PreArbDay:  -1,
		BasisTxnID: basis,
	}
	m.advance(now)
	return nil
}

func (m *Model) transition(now int, caseID string, want chargeback.State,
	apply func(c *chargeback.Case)) error {
	if caseID == "" {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	c, ok := m.cases[caseID]
	if !ok {
		return &chargeback.Error{Code: chargeback.ErrCaseNotFound}
	}
	if m.state(c, now) != want {
		return &chargeback.Error{Code: chargeback.ErrInvalidState}
	}
	apply(c)
	m.advance(now)
	return nil
}

// Respond 商户应诉。
func (m *Model) Respond(now int, caseID string) error {
	return m.transition(now, caseID, chargeback.StateOpened, func(c *chargeback.Case) {
		c.RespondDay = now
	})
}

// Accept 发卡行接受应诉。
func (m *Model) Accept(now int, caseID string) error {
	return m.transition(now, caseID, chargeback.StateAwaitingReview, func(c *chargeback.Case) {
		c.Accepted = true
		c.MoneySettled = true
	})
}

// PreArbitrate 发卡行发起预仲裁。
func (m *Model) PreArbitrate(now int, caseID string) error {
	return m.transition(now, caseID, chargeback.StateAwaitingReview, func(c *chargeback.Case) {
		c.PreArbDay = now
	})
}

// Rule 终局裁决。
func (m *Model) Rule(now int, caseID string, outcome chargeback.Outcome) error {
	if !outcome.Valid() {
		return &chargeback.Error{Code: chargeback.ErrInvalidArgument}
	}
	return m.transition(now, caseID, chargeback.StatePreArbitration, func(c *chargeback.Case) {
		c.Ruling = outcome
		c.MoneySettled = true
	})
}

// ledger 全量扫描案件，重算某一时刻的全部资金账。
type ledger struct {
	merchant map[string]int64
	issuer   int64
	pending  int64
	fees     int64
}

func (m *Model) computeLedger(now int) ledger {
	l := ledger{merchant: make(map[string]int64)}
	for _, c := range m.cases {
		l.merchant[c.MerchantID] -= c.Amount // 提起即刻扣回
		switch m.state(c, now) {
		case chargeback.StateClosedMerchantWin:
			l.merchant[c.MerchantID] += c.Amount // 款项返还
		case chargeback.StateClosedIssuerWin:
			l.issuer += c.Amount // 款项归发卡行
		default:
			l.pending += c.Amount // 待决扣回款
		}
		if c.Ruling == chargeback.OutcomeMerchantWin {
			l.issuer -= m.cfg.ArbitrationFee
			l.fees += m.cfg.ArbitrationFee
		} else if c.Ruling == chargeback.OutcomeIssuerWin {
			l.merchant[c.MerchantID] -= m.cfg.ArbitrationFee
			l.fees += m.cfg.ArbitrationFee
		}
	}
	return l
}

// MerchantBalance 查询商户余额。
func (m *Model) MerchantBalance(now int, merchantID string) (int64, error) {
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	bal := m.computeLedger(now).merchant[merchantID]
	m.advance(now)
	return bal, nil
}

// IssuerBalance 查询发卡行账。
func (m *Model) IssuerBalance(now int) (int64, error) {
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	bal := m.computeLedger(now).issuer
	m.advance(now)
	return bal, nil
}

// PendingHeld 查询待决扣回款。
func (m *Model) PendingHeld(now int) (int64, error) {
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	p := m.computeLedger(now).pending
	m.advance(now)
	return p, nil
}

// TotalFees 查询累计仲裁费。
func (m *Model) TotalFees(now int) (int64, error) {
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	f := m.computeLedger(now).fees
	m.advance(now)
	return f, nil
}

// DisputableAmount 查询交易可拒付余额。
func (m *Model) DisputableAmount(now int, txnID string) (int64, error) {
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	if _, ok := m.txns[txnID]; !ok {
		return 0, &chargeback.Error{Code: chargeback.ErrTransactionNotFound}
	}
	d := m.disputable(now, txnID)
	m.advance(now)
	return d, nil
}

// CaseState 查询案件状态。
func (m *Model) CaseState(now int, caseID string) (chargeback.State, error) {
	if err := m.checkClock(now); err != nil {
		return chargeback.StateOpened, err
	}
	c, ok := m.cases[caseID]
	if !ok {
		return chargeback.StateOpened, &chargeback.Error{Code: chargeback.ErrCaseNotFound}
	}
	s := m.state(c, now)
	m.advance(now)
	return s, nil
}

// LedgerSummary 全量重算账目快照，与引擎的增量物化结果对照。
func (m *Model) LedgerSummary(now int) (chargeback.LedgerSummary, error) {
	if err := m.checkClock(now); err != nil {
		return chargeback.LedgerSummary{}, err
	}
	l := m.computeLedger(now)
	sum := chargeback.LedgerSummary{
		Issuer:  l.issuer,
		Pending: l.pending,
		Fees:    l.fees,
	}
	for _, bal := range l.merchant {
		sum.MerchantTotal += bal
	}
	m.advance(now)
	return sum, nil
}
