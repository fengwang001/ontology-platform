package coord

import "ontology/ledger"

// Begin 登记全局事务与其分支（1..16 个，br 不可重复）。xid 已存在报 ErrExists。
func (c *Coordinator) Begin(xid []byte, branches []BranchSpec, now int64) error {
	if len(xid) == 0 || !validBranches(branches) {
		return ledger.ErrParam
	}
	if err := checkClock(now); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	xk := string(xid)
	bs := make([]BranchSpec, len(branches))
	for i, b := range branches {
		bs[i] = BranchSpec{
			BR:     append([]byte(nil), b.BR...),
			Acct:   append([]byte(nil), b.Acct...),
			Amount: b.Amount,
		}
	}
	tmClock := c.tm.Clock()
	if now < tmClock {
		return ledger.ErrClock
	}
	if _, ok := c.txns[xk]; ok {
		return ledger.ErrExists
	}
	t := &txn{xid: xk, branches: bs, tried: map[string]bool{}, broken: map[string]bool{}}
	c.txns[xk] = t
	t.logs = append(t.logs, logEntry{kind: logBegin})
	// Begin 成功推进 tcc 时钟：Begin 日志是可恢复事件。
	if err := c.tm.AdvanceClock(now); err != nil {
		return err
	}
	c.crash(xk, "begin", 0) // Begin 日志落盘后的崩溃点
	return nil
}

// Run 依次 Try 各分支；全部成功写 Commit，任一失败立即停止并写 Abort。
// 决议写定后不可改，重复 Run 返回已写决议与 nil。
func (c *Coordinator) Run(xid []byte, now int64) (Decision, error) {
	if len(xid) == 0 {
		return DecisionNone, ledger.ErrParam
	}
	if err := checkClock(now); err != nil {
		return DecisionNone, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.tm.Clock() {
		return DecisionNone, ledger.ErrClock
	}
	t, ok := c.txns[string(xid)]
	if !ok {
		return DecisionNone, ledger.ErrNoTx
	}
	if t.decision != DecisionNone {
		return t.decision, nil
	}
	if t.ran {
		return t.decision, nil
	}
	for i, b := range t.branches {
		err := c.tm.Try([]byte(t.xid), b.BR, b.Acct, b.Amount, now)
		if err != nil {
			t.decision = DecisionAbort
			t.logs = append(t.logs, logEntry{kind: logDecision, decision: DecisionAbort})
			t.ran = true
			c.crash(t.xid, "decision", i)
			return DecisionAbort, nil
		}
		t.tried[string(b.BR)] = true
		t.logs = append(t.logs, logEntry{kind: logTry, br: string(b.BR)})
		c.crash(t.xid, "try", i) // 第 i 个 Try 成功且日志落盘后的崩溃点
	}
	t.decision = DecisionCommit
	t.logs = append(t.logs, logEntry{kind: logDecision, decision: DecisionCommit})
	t.ran = true
	c.crash(t.xid, "decision", len(t.branches)-1)
	return DecisionCommit, nil
}

// Decision 返回已写决议；未写返回 DecisionNone。事务不存在报 ErrNoTx。
func (c *Coordinator) Decision(xid []byte) (Decision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.txns[string(xid)]
	if !ok {
		return DecisionNone, ledger.ErrNoTx
	}
	return t.decision, nil
}
