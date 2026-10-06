package creditcard

import "sync"

// Service is a concurrency-safe collection of credit-card accounts with a
// single global clock: every accepted operation must carry a `now` not
// smaller than the last accepted operation's `now`. Rejected operations
// change neither state nor clock.
type Service struct {
	mu       sync.RWMutex
	accounts map[string]*account
	clock    int64
	clockSet bool
}

// NewService returns an empty service.
func NewService() *Service {
	return &Service{accounts: make(map[string]*account)}
}

// checkClock returns ErrClockRollback if now is before the service clock.
// Callers must hold the write lock.
func (s *Service) checkClock(now int64) error {
	if s.clockSet && now < s.clock {
		return ErrClockRollback
	}
	return nil
}

// advanceClock must only be called after the operation was accepted.
func (s *Service) advanceClock(now int64) {
	s.clock = now
	s.clockSet = true
}

// CreateAccount opens an account with the given params at day `now`.
func (s *Service) CreateAccount(id string, p Params, now int64) error {
	if id == "" || !p.valid() {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.accounts[id]; ok {
		return ErrAccountExists
	}
	s.accounts[id] = newAccount(p, now)
	s.advanceClock(now)
	return nil
}

// Charge books a purchase of amount into category c of account id.
func (s *Service) Charge(id string, c Category, amount, now int64) error {
	if amount <= 0 || !c.Valid() {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	a, ok := s.accounts[id]
	if !ok {
		return ErrAccountNotFound
	}
	a.charge(c, amount, now)
	s.advanceClock(now)
	return nil
}

// Pay accepts a repayment and allocates it immediately, returning the
// allocation record.
func (s *Service) Pay(id string, amount, now int64) (PaymentRecord, error) {
	if amount <= 0 {
		return PaymentRecord{}, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return PaymentRecord{}, err
	}
	a, ok := s.accounts[id]
	if !ok {
		return PaymentRecord{}, ErrAccountNotFound
	}
	rec := a.pay(amount, now)
	s.advanceClock(now)
	return rec, nil
}

// Bill closes the current period of account id at day `now`. The billing
// day must be later than the previous bill's due date.
func (s *Service) Bill(id string, now int64) (Bill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return Bill{}, err
	}
	a, ok := s.accounts[id]
	if !ok {
		return Bill{}, ErrAccountNotFound
	}
	if n := len(a.bills); n > 0 && now <= a.bills[n-1].DueDate {
		return Bill{}, ErrBillingTooEarly
	}
	b := a.bill(now)
	s.advanceClock(now)
	return b, nil
}

// Balances returns the three category balances of account id.
func (s *Service) Balances(id string) ([NumCategories]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	if !ok {
		return [NumCategories]int64{}, ErrAccountNotFound
	}
	return a.balance, nil
}

// Overpayment returns the overpayment (溢缴款) of account id.
func (s *Service) Overpayment(id string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	if !ok {
		return 0, ErrAccountNotFound
	}
	return a.overpayment, nil
}

// Bills returns a copy of the statement history of account id.
func (s *Service) Bills(id string) ([]Bill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	out := make([]Bill, len(a.bills))
	copy(out, a.bills)
	return out, nil
}

// Payments returns a copy of the payment allocation records of account id.
func (s *Service) Payments(id string) ([]PaymentRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	out := make([]PaymentRecord, len(a.payments))
	copy(out, a.payments)
	return out, nil
}
