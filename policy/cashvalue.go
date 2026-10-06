package policy

// CashValue 计算任意计算日的现金价值，loan 为外部提供的未偿借款本息（分）。
// 开销为 O(1)：累计实缴滚动维护，比例表按下标直取。
func (e *Engine) CashValue(id string, day, loan int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if day < 0 || loan < 0 {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := e.policies[id]
	if !ok {
		return 0, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if p.terminated {
		return 0, newErr(ErrTerminated, "已终态")
	}
	if day < p.effDate {
		return 0, newErr(ErrInvalidParam, "参数非法")
	}
	return p.cashValueAt(day, loan), nil
}
