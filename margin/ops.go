package margin

// Deposit 为账户 a 存入 x。账户不存在时先创建。
// 拒绝条件（按序只报第一个）：参数非法（空编号、x 越界、存入后 M 越界）。
func (e *Engine) Deposit(a string, x int64) error {
	if a == "" || x < 1 || x > maxAmount {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acct := e.accounts[a]
	if acct == nil {
		acct = &Account{}
		e.accounts[a] = acct
	}
	if acct.M+x > maxMargin {
		return ErrInvalidParam
	}
	acct.M += x
	return nil
}

// Open 以方向 dir、数量 n、价格 p 为账户 a 开仓。
// 要求账户无仓位或方向与现有持仓相同；开仓后须满足
// M >= ceil(|C|*I/10000)。开仓不收费也不检查强平。
func (e *Engine) Open(a string, dir Side, n, p int64) error {
	if a == "" || (dir != Long && dir != Short) ||
		n < 1 || n > maxPrice || p < 1 || p > maxPrice {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acct := e.accounts[a]
	if acct == nil {
		return ErrAccountNotFound
	}
	if acct.Q != 0 && (acct.Q > 0) != (dir == Long) {
		return ErrPositionConflict
	}
	signed := dir.sign() * n
	if abs64(acct.Q+signed) > maxPosition {
		return ErrInvalidParam
	}
	newC := acct.C + signed*p
	if acct.M < ceilDiv(abs64(newC)*e.i, rateBase) {
		return ErrInsufficientFunds
	}
	acct.Q += signed
	acct.C = newC
	return nil
}

// Close 以价格 p 全部平仓。权益 E = M + q*p - C，M 变为 max(E, 0)；
// 若 E < 0，亏空 -E 先由保险基金吸收，其余记入坏账。
// 不触发自动减仓，不收平仓费。
func (e *Engine) Close(a string, p int64) error {
	if a == "" || p < 1 || p > maxPrice {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acct := e.accounts[a]
	if acct == nil {
		return ErrAccountNotFound
	}
	if acct.Q == 0 {
		return ErrPositionConflict
	}
	equity := acct.M + acct.Q*p - acct.C
	if equity >= 0 {
		acct.M = equity
	} else {
		acct.M = 0
		d := -equity
		u := min(e.z, d)
		e.z -= u
		e.b += d - u
	}
	acct.Q = 0
	acct.C = 0
	return nil
}

// Withdraw 从账户 a 取出 x，仅当无仓位时可用，且 x 不大于 M。
func (e *Engine) Withdraw(a string, x int64) error {
	if a == "" || x < 1 || x > maxAmount {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acct := e.accounts[a]
	if acct == nil {
		return ErrAccountNotFound
	}
	if acct.Q != 0 {
		return ErrPositionConflict
	}
	if x > acct.M {
		return ErrInsufficientFunds
	}
	acct.M -= x
	return nil
}

// GetAccount 返回账户 a 的 (M, q, C) 快照与是否存在。
func (e *Engine) GetAccount(a string) (m, q, c int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	acct := e.accounts[a]
	if acct == nil {
		return 0, 0, 0, false
	}
	return acct.M, acct.Q, acct.C, true
}

// InsuranceFund 返回保险基金 Z。
func (e *Engine) InsuranceFund() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.z
}

// BadDebt 返回坏账累计 B。
func (e *Engine) BadDebt() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.b
}

// MarkPrice 返回最近一次标记价格；从未 Mark 时 ok 为 false。
func (e *Engine) MarkPrice() (price int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mark, e.hasMark
}
