package chargeback

// AddTransaction 登记一笔交易。结算日不得晚于 now（结算后方可被拒付的前提）。
// 错误优先级：参数非法 > 时钟回退。
func (e *Engine) AddTransaction(now int, txn Transaction) error {
	if txn.ID == "" || txn.CardID == "" || txn.MerchantID == "" || txn.Amount <= 0 {
		return newError(ErrInvalidArgument, "交易字段非法: %+v", txn)
	}
	if now < txn.SettleDay {
		return newError(ErrInvalidArgument, "交易 %s 结算日 %d 晚于当前时刻 %d", txn.ID, txn.SettleDay, now)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.txns[txn.ID]; dup {
		return newError(ErrInvalidArgument, "交易 %s 已登记", txn.ID)
	}
	v, err := e.begin(now)
	if err != nil {
		return err
	}
	t := txn
	e.txns[t.ID] = &t
	key := profileKey{cardID: t.CardID, merchantID: t.MerchantID, amount: t.Amount}
	e.txnIDsByProfile[key] = append(e.txnIDsByProfile[key], t.ID)
	v.commit(now)
	return nil
}

// OpenDispute 提起拒付。成功后即刻从商户可用余额扣回拒付金额。
// 错误优先级：参数非法 > 时钟回退 > 交易不存在 > 窗口已过 >
// 重复提起 > 超出可拒付余额 > 无依据。
func (e *Engine) OpenDispute(now int, caseID, txnID string, reason Reason, amount int64) error {
	if caseID == "" || txnID == "" || !reason.Valid() || amount <= 0 {
		return newError(ErrInvalidArgument, "提起参数非法: case=%q txn=%q reason=%d amount=%d", caseID, txnID, reason, amount)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.cases[caseID]; dup {
		return newError(ErrInvalidArgument, "案件 %s 已存在", caseID)
	}
	v, err := e.begin(now)
	if err != nil {
		return err
	}
	txn, ok := e.txns[txnID]
	if !ok {
		v.reject()
		return newError(ErrTransactionNotFound, "交易 %s 不存在", txnID)
	}
	if window := e.cfg.WindowDays(reason); now-txn.SettleDay > window {
		v.reject()
		return newError(ErrWindowExpired, "交易 %s 提起日 %d 超出 %d 天窗口（结算日 %d）", txnID, now, window, txn.SettleDay)
	}
	for _, id := range e.caseIDsByTxnReason[txnReasonKey{txnID: txnID, reason: reason}] {
		if e.cases[id].State(now, e.cfg) == StateClosedMerchantWin {
			v.reject()
			return newError(ErrDuplicateCase, "交易 %s 原因 %d 已有商户胜案件 %s", txnID, reason, id)
		}
	}
	if disputable := txn.Amount - v.heldOf(txnID); amount > disputable {
		v.reject()
		return newError(ErrExceedsDisputable, "金额 %d 超出交易 %s 可拒付余额 %d", amount, txnID, disputable)
	}
	basis := ""
	if reason == ReasonDuplicate {
		basis = e.findBasis(now, txn)
		if basis == "" {
			v.reject()
			return newError(ErrNoBasis, "交易 %s 无合格的重复扣款依据交易", txnID)
		}
	}
	c := &Case{
		ID:         caseID,
		TxnID:      txnID,
		MerchantID: txn.MerchantID,
		Reason:     reason,
		Amount:     amount,
		OpenDay:    now,
		RespondDay: noDay,
		PreArbDay:  noDay,
		BasisTxnID: basis,
	}
	e.cases[caseID] = c
	k := txnReasonKey{txnID: txnID, reason: reason}
	e.caseIDsByTxnReason[k] = append(e.caseIDsByTxnReason[k], caseID)
	if basis != "" {
		e.caseIDsByBasis[basis] = append(e.caseIDsByBasis[basis], caseID)
	}
	e.merchantBal[txn.MerchantID] -= amount
	e.pendingHeld += amount
	e.heldByTxn[txnID] += amount
	e.sched.schedule(now+e.cfg.ResponseWindowDays+1, eventForfeit, caseID)
	v.commit(now)
	return nil
}

// findBasis 为重复扣款寻找依据交易：同卡、同商户、同金额、更早结算、
// 结算日之差不超过 DuplicateMatchDays，且未被其他未以商户胜告终的案件占用。
// 多个候选时取结算日最晚者，并列取交易 ID 字典序最小者，保证确定性。
func (e *Engine) findBasis(now int, txn *Transaction) string {
	key := profileKey{cardID: txn.CardID, merchantID: txn.MerchantID, amount: txn.Amount}
	best := ""
	for _, id := range e.txnIDsByProfile[key] {
		if id == txn.ID {
			continue
		}
		cand := e.txns[id]
		diff := txn.SettleDay - cand.SettleDay
		if diff <= 0 || diff > e.cfg.DuplicateMatchDays {
			continue
		}
		if e.basisOccupied(now, id) {
			continue
		}
		if best == "" || cand.SettleDay > e.txns[best].SettleDay ||
			(cand.SettleDay == e.txns[best].SettleDay && id < best) {
			best = id
		}
	}
	return best
}

// basisOccupied 报告依据交易是否被某个未以商户胜告终的重复扣款案件占用。
func (e *Engine) basisOccupied(now int, basisTxnID string) bool {
	for _, id := range e.caseIDsByBasis[basisTxnID] {
		if e.cases[id].State(now, e.cfg) != StateClosedMerchantWin {
			return true
		}
	}
	return false
}

// transition 是应诉/接受/预仲裁/裁决共用的校验骨架。
// 错误优先级：参数非法 > 时钟回退 > 案件不存在 > 当前状态不允许。
func (e *Engine) transition(now int, caseID string, want State, apply func(c *Case)) error {
	if caseID == "" {
		return newError(ErrInvalidArgument, "案件 ID 为空")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.begin(now)
	if err != nil {
		return err
	}
	c, ok := e.cases[caseID]
	if !ok {
		v.reject()
		return newError(ErrCaseNotFound, "案件 %s 不存在", caseID)
	}
	if got := c.State(now, e.cfg); got != want {
		v.reject()
		return newError(ErrInvalidState, "案件 %s 当前状态 %s，期望 %s", caseID, got, want)
	}
	apply(c)
	v.commit(now)
	return nil
}

// Respond 商户在应诉期内提交应诉材料，案件转入待审阅。每案仅可应诉一次。
func (e *Engine) Respond(now int, caseID string) error {
	return e.transition(now, caseID, StateOpened, func(c *Case) {
		c.RespondDay = now
		e.sched.schedule(now+e.cfg.ReviewWindowDays+1, eventAutoAccept, caseID)
	})
}

// Accept 发卡行在审阅期内接受应诉：商户胜，款项返还。
// 与逾期默认接受的资金结果完全相同。
func (e *Engine) Accept(now int, caseID string) error {
	return e.transition(now, caseID, StateAwaitingReview, func(c *Case) {
		c.Accepted = true
		c.MoneySettled = true
		e.merchantBal[c.MerchantID] += c.Amount
		e.pendingHeld -= c.Amount
		e.heldByTxn[c.TxnID] -= c.Amount
	})
}

// PreArbitrate 发卡行在审阅期内发起预仲裁，每案仅限一次。
// 发起后应诉与接受不可再用，裁决无时限。
func (e *Engine) PreArbitrate(now int, caseID string) error {
	return e.transition(now, caseID, StateAwaitingReview, func(c *Case) {
		c.PreArbDay = now
	})
}

// Rule 对预仲裁中的案件作出终局裁决，并结算款项与仲裁费。
func (e *Engine) Rule(now int, caseID string, outcome Outcome) error {
	if !outcome.Valid() {
		return newError(ErrInvalidArgument, "裁决结果非法: %d", outcome)
	}
	return e.transition(now, caseID, StatePreArbitration, func(c *Case) {
		c.Ruling = outcome
		c.MoneySettled = true
		e.pendingHeld -= c.Amount
		switch outcome {
		case OutcomeMerchantWin:
			e.merchantBal[c.MerchantID] += c.Amount
			e.heldByTxn[c.TxnID] -= c.Amount
			e.issuerBal -= e.cfg.ArbitrationFee
		case OutcomeIssuerWin:
			e.issuerBal += c.Amount
			e.merchantBal[c.MerchantID] -= e.cfg.ArbitrationFee
		}
		e.feesTotal += e.cfg.ArbitrationFee
	})
}
