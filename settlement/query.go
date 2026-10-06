package settlement

func (s *System) Position(accountID AccountID) (Position, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	account, ok := s.accounts[accountID]
	if !ok {
		return Position{}, ErrAccountNotFound
	}
	holdings := make(map[SecurityID]int64, len(account.holdings))
	for security, quantity := range account.holdings {
		holdings[security] = quantity
	}
	return Position{Cash: account.cash, Holdings: holdings}, nil
}

func (s *System) PenaltyBalances(accountID AccountID) (Balances, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	account, ok := s.accounts[accountID]
	if !ok {
		return Balances{}, ErrAccountNotFound
	}
	return Balances{Payable: account.penaltyPayable, Receivable: account.penaltyReceivable}, nil
}

func (s *System) CompensationBalances(accountID AccountID) (Balances, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	account, ok := s.accounts[accountID]
	if !ok {
		return Balances{}, ErrAccountNotFound
	}
	return Balances{Payable: account.compPayable, Receivable: account.compReceivable}, nil
}

func (s *System) Order(id uint64) (OrderView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	order, ok := s.orders[id]
	if !ok {
		return OrderView{}, ErrOrderNotFound
	}
	return orderView(order), nil
}

func orderView(order *Order) OrderView {
	return OrderView{
		ID:            order.Input.ID,
		Security:      order.Input.Security,
		Buyer:         order.Input.Buyer,
		Seller:        order.Input.Seller,
		Quantity:      order.Input.Quantity,
		Price:         order.Input.Price,
		SettlementDay: order.Input.SettlementDay,
		AllowPartial:  order.Input.AllowPartial,
		Delivered:     order.Delivered,
		Remaining:     order.Remaining,
		Status:        order.Status,
		FailedDays:    order.FailedDays,
	}
}
