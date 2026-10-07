package payledger

// 本文件实现全部变更操作与查询的内部逻辑（调用方已持有锁）。
// 每个操作先按固定优先级顺序做全部校验，全部通过后才修改状态，
// 因此被拒绝的操作不留任何痕迹（状态与时钟均不变）。

// checkClock 校验并推进时钟。被拒绝的操作不得调用 accept。
func (l *Ledger) checkClock(now int64) error {
	if l.hasClock && now < l.lastNow {
		return newErr(ErrClockRollback, "时钟回退: now=%d 小于上次被接受操作的 now=%d", now, l.lastNow)
	}
	return nil
}

func (l *Ledger) accept(now int64) {
	l.lastNow = now
	l.hasClock = true
}

func (l *Ledger) createAccount(accountID string, creditLimit, now int64) error {
	if accountID == "" || creditLimit < 0 {
		return newErr(ErrInvalidParam, "开户参数非法: id=%q credit=%d", accountID, creditLimit)
	}
	if _, ok := l.accounts[accountID]; ok {
		return newErr(ErrInvalidParam, "账户已存在: %s", accountID)
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	l.accounts[accountID] = &account{id: accountID, credit: creditLimit, tracker: newHoldTracker()}
	l.accept(now)
	return nil
}

func (l *Ledger) adjustCredit(accountID string, newLimit, now int64) error {
	if newLimit < 0 {
		return newErr(ErrInvalidParam, "信用额度非法: %d", newLimit)
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	acct, ok := l.accounts[accountID]
	if !ok {
		return newErr(ErrAccountNotFound, "账户不存在: %s", accountID)
	}
	if newLimit-acct.posted-acct.tracker.active(now) < 0 {
		return newErr(ErrInsufficientFunds,
			"调低额度将使可用额度为负: newLimit=%d posted=%d holds=%d",
			newLimit, acct.posted, acct.tracker.active(now))
	}
	acct.credit = newLimit
	l.accept(now)
	return nil
}

func (l *Ledger) authorize(accountID, authID string, amount, now int64) error {
	if accountID == "" || authID == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "授权参数非法: account=%q auth=%q amount=%d", accountID, authID, amount)
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	if _, dup := l.auths[authID]; dup {
		return newErr(ErrDuplicateAuthID, "授权编号重复: %s", authID)
	}
	acct, ok := l.accounts[accountID]
	if !ok {
		return newErr(ErrAccountNotFound, "账户不存在: %s", accountID)
	}
	if avail := acct.credit - acct.posted - acct.tracker.active(now); avail < amount {
		return newErr(ErrInsufficientFunds, "可用额度不足: 可用=%d 需要=%d", avail, amount)
	}
	expiry := now + l.cfg.ExpiryDays
	l.auths[authID] = &authorization{
		id: authID, accountID: accountID, cumAuth: amount, expiryDay: expiry, active: true,
	}
	acct.tracker.add(expiry, amount)
	l.accept(now)
	return nil
}

func (l *Ledger) increment(authID string, amount, now int64) error {
	if authID == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "增量参数非法: auth=%q amount=%d", authID, amount)
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	a, ok := l.auths[authID]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在: %s", authID)
	}
	if a.statusAt(now) != StatusActive {
		return newErr(ErrAuthTerminated, "授权已终结(%s): %s", a.statusAt(now), authID)
	}
	acct := l.accounts[a.accountID]
	if avail := acct.credit - acct.posted - acct.tracker.active(now); avail < amount {
		// 额外可用额度仅按增量金额校验，不重复计算原持有。
		return newErr(ErrInsufficientFunds, "可用额度不足: 可用=%d 需要=%d", avail, amount)
	}
	oldRemain := a.remainingHold(now)
	acct.tracker.remove(a.expiryDay, oldRemain)
	a.cumAuth += amount
	a.expiryDay = now + l.cfg.ExpiryDays // 有效期重置为增量日起 E 天（含）
	acct.tracker.add(a.expiryDay, a.remainingHold(now))
	l.accept(now)
	return nil
}

func (l *Ledger) capture(authID string, amount int64, final bool, now int64) error {
	if authID == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "捕获参数非法: auth=%q amount=%d", authID, amount)
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	a, ok := l.auths[authID]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在: %s", authID)
	}
	if a.statusAt(now) != StatusActive {
		return newErr(ErrAuthTerminated, "授权已终结(%s): %s", a.statusAt(now), authID)
	}
	if a.captured+amount > allowedCapture(a.cumAuth, l.cfg.ToleranceBps) {
		return newErr(ErrOverTolerance,
			"超容差: 累计捕获=%d + 本次=%d > 上限=%d",
			a.captured, amount, allowedCapture(a.cumAuth, l.cfg.ToleranceBps))
	}
	acct := l.accounts[a.accountID]
	remain := a.remainingHold(now)
	excess := amount - remain // 超出剩余持有的部分须由当前可用额度覆盖
	if excess > 0 {
		if avail := acct.credit - acct.posted - acct.tracker.active(now); avail < excess {
			return newErr(ErrInsufficientFunds, "可用额度不足: 可用=%d 超出持有部分=%d", avail, excess)
		}
	}
	// 入账与持有减少在同一把锁内同时生效，对外不可观察到中间态。
	if remain > 0 {
		acct.tracker.remove(a.expiryDay, min(remain, amount))
	}
	acct.posted += amount
	a.captured += amount
	if final {
		if left := a.remainingHold(now); left > 0 {
			acct.tracker.remove(a.expiryDay, left) // 终捕立即释放剩余持有
		}
		a.active = false
		a.terminal = StatusFinalCaptured
	}
	l.accept(now)
	return nil
}

func (l *Ledger) void(authID string, now int64) error {
	if authID == "" {
		return newErr(ErrInvalidParam, "撤销参数非法: 空授权编号")
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	a, ok := l.auths[authID]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在: %s", authID)
	}
	if a.statusAt(now) != StatusActive {
		return newErr(ErrAuthTerminated, "授权已终结(%s): %s", a.statusAt(now), authID)
	}
	acct := l.accounts[a.accountID]
	if remain := a.remainingHold(now); remain > 0 {
		acct.tracker.remove(a.expiryDay, remain)
	}
	a.active = false
	a.terminal = StatusVoided
	l.accept(now)
	return nil
}

func (l *Ledger) refund(authID string, amount, now int64) error {
	if authID == "" || amount <= 0 {
		return newErr(ErrInvalidParam, "退款参数非法: auth=%q amount=%d", authID, amount)
	}
	if err := l.checkClock(now); err != nil {
		return err
	}
	a, ok := l.auths[authID]
	if !ok {
		return newErr(ErrAuthNotFound, "授权不存在: %s", authID)
	}
	if refundable := a.captured - a.refunded; amount > refundable {
		return newErr(ErrRefundExceeds, "超出可退额: 可退=%d 请求=%d", refundable, amount)
	}
	// 退款只减少已入账余额：不恢复持有、不改变状态/有效期/容差上限。
	l.accounts[a.accountID].posted -= amount
	a.refunded += amount
	l.accept(now)
	return nil
}

func (l *Ledger) available(accountID string, now int64) (int64, error) {
	acct, ok := l.accounts[accountID]
	if !ok {
		return 0, newErr(ErrAccountNotFound, "账户不存在: %s", accountID)
	}
	return acct.credit - acct.posted - acct.tracker.active(now), nil
}

func (l *Ledger) snapshot(authID string, now int64) (AuthSnapshot, error) {
	a, ok := l.auths[authID]
	if !ok {
		return AuthSnapshot{}, newErr(ErrAuthNotFound, "授权不存在: %s", authID)
	}
	return AuthSnapshot{
		AuthID:        a.id,
		AccountID:     a.accountID,
		Status:        a.statusAt(now),
		RemainingHold: a.remainingHold(now),
		CumAuth:       a.cumAuth,
		Captured:      a.captured,
		Refunded:      a.refunded,
		ExpiryDay:     a.expiryDay,
	}, nil
}
