package chargeback

// 查询与操作共享同一单调时钟：查询的 now 不得早于引擎当前时刻，
// 查询会物化到期事件（缓存加速），但不产生任何业务事件。
// 任意查询结果都只是“已接受操作序列 + 查询 now”的函数。

// MerchantBalance 查询商户可用余额（允许为负）。
func (e *Engine) MerchantBalance(now int, merchantID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return 0, err
	}
	bal := v.merchantBalance(merchantID)
	v.commit(now)
	return bal, nil
}

// IssuerBalance 查询发卡行账：发卡行胜所得款减去发卡行承担的仲裁费。
func (e *Engine) IssuerBalance(now int) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return 0, err
	}
	bal := e.issuerBal + v.delta.issuer
	v.commit(now)
	return bal, nil
}

// PendingHeld 查询待决扣回款：仍在进行中的案件已扣回而尚未归属的总额。
func (e *Engine) PendingHeld(now int) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return 0, err
	}
	p := e.pendingHeld + v.delta.pending
	v.commit(now)
	return p, nil
}

// TotalFees 查询累计仲裁费（已流出系统，不参与资金守恒式）。
func (e *Engine) TotalFees(now int) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return 0, err
	}
	f := e.feesTotal
	v.commit(now)
	return f, nil
}

// DisputableAmount 查询交易在 now 时刻的可拒付余额：
// 交易额减去所有进行中或以发卡行胜告终的案件金额之和。
func (e *Engine) DisputableAmount(now int, txnID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return 0, err
	}
	txn, ok := e.txns[txnID]
	if !ok {
		v.reject()
		return 0, newError(ErrTransactionNotFound, "交易 %s 不存在", txnID)
	}
	d := txn.Amount - v.heldOf(txnID)
	v.commit(now)
	return d, nil
}

// CaseState 查询案件在 now 时刻的推导状态。
func (e *Engine) CaseState(now int, caseID string) (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return StateOpened, err
	}
	c, ok := e.cases[caseID]
	if !ok {
		v.reject()
		return StateOpened, newError(ErrCaseNotFound, "案件 %s 不存在", caseID)
	}
	s := c.State(now, e.cfg)
	v.commit(now)
	return s, nil
}

// LedgerSummary 是资金守恒校验用的账目快照。
type LedgerSummary struct {
	MerchantTotal int64 // 全部商户余额之和
	Issuer        int64 // 发卡行账
	Pending       int64 // 待决扣回款
	Fees          int64 // 累计仲裁费
}

// Balanced 报告资金是否守恒：商户余额变动、发卡行账与待决扣回款的
// 代数和为零（仲裁费除外，即加回累计仲裁费后为零）。
func (s LedgerSummary) Balanced() bool {
	return s.MerchantTotal+s.Issuer+s.Pending+s.Fees == 0
}

// LedgerSummary 返回当前账目快照，用于守恒校验与对账。
func (e *Engine) LedgerSummary(now int) (LedgerSummary, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return LedgerSummary{}, err
	}
	sum := LedgerSummary{
		Issuer:  e.issuerBal + v.delta.issuer,
		Pending: e.pendingHeld + v.delta.pending,
		Fees:    e.feesTotal,
	}
	// 商户集合只增不减，汇总成本随商户数增长而非案件/交易数。
	merchants := make(map[string]struct{}, len(e.merchantBal)+len(v.delta.merchant))
	for m := range e.merchantBal {
		merchants[m] = struct{}{}
	}
	for m := range v.delta.merchant {
		merchants[m] = struct{}{}
	}
	for m := range merchants {
		sum.MerchantTotal += v.merchantBalance(m)
	}
	v.commit(now)
	return sum, nil
}
