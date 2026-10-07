package ncdengine

func (e *Engine) Clock() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.day
}

func (e *Engine) Level(customerID string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	customer, ok := e.accounts[customerID]
	if !ok {
		return 0, ErrCustomerNotFound
	}
	term, ok := customer.latestTerm()
	if !ok {
		return 0, ErrCustomerNotFound
	}
	return term.RenewalLevel, nil
}

func (e *Engine) Terms(customerID string) ([]PolicyTerm, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	customer, ok := e.accounts[customerID]
	if !ok {
		return nil, ErrCustomerNotFound
	}
	terms := append([]PolicyTerm(nil), customer.terms...)
	return terms, nil
}

func (e *Engine) Claims(customerID string) ([]Claim, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	customer, ok := e.accounts[customerID]
	if !ok {
		return nil, ErrCustomerNotFound
	}
	claims := make([]Claim, 0, len(customer.claims))
	for _, claim := range customer.claims {
		claims = append(claims, claim)
	}
	return claims, nil
}

func (e *Engine) PremiumDeltas(customerID string) ([]PremiumDelta, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	customer, ok := e.accounts[customerID]
	if !ok {
		return nil, ErrCustomerNotFound
	}
	deltas := append([]PremiumDelta(nil), customer.deltas...)
	return deltas, nil
}
