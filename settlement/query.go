package settlement

// query.go —— 只读查询；查询不修改任何状态。

// QueryAccount 返回账户头寸与应付应收的深拷贝快照。
func (s *System) QueryAccount(name string) (AccountView, error) {
	if name == "" {
		return AccountView{}, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[name]
	if !ok {
		return AccountView{}, ErrAccountMissing
	}
	secs := make(map[int64]int64, len(a.securities))
	for k, v := range a.securities {
		secs[k] = v
	}
	return AccountView{
		Name:           a.name,
		Securities:     secs,
		Cash:           a.cash,
		FeePayable:     a.feePay,
		FeeReceivable:  a.feeRecv,
		CompPayable:    a.compPay,
		CompReceivable: a.compRecv,
	}, nil
}

// QueryOrder 返回指令状态快照。
func (s *System) QueryOrder(id int64) (OrderView, error) {
	if id <= 0 {
		return OrderView{}, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ins, ok := s.orders[id]
	if !ok {
		return OrderView{}, ErrOrderNotFound
	}
	return OrderView{
		Order:        ins.Order,
		DeliveredQty: ins.delivered,
		FailedDays:   ins.failDays,
		Status:       ins.status,
	}, nil
}
