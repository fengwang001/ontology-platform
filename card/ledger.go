package card

import "sync"

// Ledger manages a set of accounts and the global acceptance clock.
//
// All methods are safe for concurrent use; the result is equivalent to
// some serial order of the calls. The clock is global: now must never be
// smaller than the now of the last accepted operation on any account
// (this is what makes the error priority "clock rollback before account
// not found" well-defined). Rejected operations change nothing, not even
// the clock.
type Ledger struct {
	mu       sync.RWMutex
	hasNow   bool
	lastNow  int64
	accounts map[string]*account
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{accounts: make(map[string]*account)}
}

// checkClock reports ErrClockRollback if now is before the last accepted
// operation. Caller must hold the lock.
func (l *Ledger) checkClock(now int64) error {
	if l.hasNow && now < l.lastNow {
		return ErrClockRollback
	}
	return nil
}

// accept advances the clock after a successful operation.
// Caller must hold the lock.
func (l *Ledger) accept(now int64) {
	l.lastNow = now
	l.hasNow = true
}

// CreateAccount creates an account with the given parameters.
//
// Errors, in priority order: ErrInvalidParam (empty id, invalid params,
// negative now), ErrClockRollback, ErrAccountExists.
func (l *Ledger) CreateAccount(id string, p Params, now int64) error {
	if id == "" || !p.valid() || now < 0 {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkClock(now); err != nil {
		return err
	}
	if _, ok := l.accounts[id]; ok {
		return ErrAccountExists
	}
	l.accounts[id] = newAccount(p, now)
	l.accept(now)
	return nil
}

// Charge posts a purchase of amount to category cat. Overpayment offsets
// the charge first; only the remainder forms balance.
//
// Errors, in priority order: ErrInvalidParam (amount <= 0, unknown
// category, negative now), ErrClockRollback, ErrAccountNotFound.
func (l *Ledger) Charge(id string, cat Category, amount int64, now int64) error {
	if amount <= 0 || !cat.Valid() || now < 0 {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkClock(now); err != nil {
		return err
	}
	a, ok := l.accounts[id]
	if !ok {
		return ErrAccountNotFound
	}
	a.charge(now, cat, amount)
	l.accept(now)
	return nil
}

// Bill generates a statement for the account on day now and returns it.
//
// Errors, in priority order: ErrInvalidParam (negative now),
// ErrClockRollback, ErrAccountNotFound, ErrBillingTooEarly.
func (l *Ledger) Bill(id string, now int64) (Bill, error) {
	if now < 0 {
		return Bill{}, ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkClock(now); err != nil {
		return Bill{}, err
	}
	a, ok := l.accounts[id]
	if !ok {
		return Bill{}, ErrAccountNotFound
	}
	if a.tooEarly(now) {
		return Bill{}, ErrBillingTooEarly
	}
	b := a.bill(now)
	l.accept(now)
	return b, nil
}

// Repay accepts a repayment of amount and allocates it immediately,
// returning the allocation record.
//
// Errors, in priority order: ErrInvalidParam (amount <= 0, negative
// now), ErrClockRollback, ErrAccountNotFound.
func (l *Ledger) Repay(id string, amount int64, now int64) (Repayment, error) {
	if amount <= 0 || now < 0 {
		return Repayment{}, ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkClock(now); err != nil {
		return Repayment{}, err
	}
	a, ok := l.accounts[id]
	if !ok {
		return Repayment{}, ErrAccountNotFound
	}
	r := a.repay(now, amount)
	l.accept(now)
	return r, nil
}

// Snapshot returns the current balances and overpayment.
func (l *Ledger) Snapshot(id string) (Snapshot, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.accounts[id]
	if !ok {
		return Snapshot{}, ErrAccountNotFound
	}
	return Snapshot{Balances: a.bal, Overpayment: a.over}, nil
}

// Bills returns a copy of all statements, oldest first.
func (l *Ledger) Bills(id string) ([]Bill, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	out := make([]Bill, len(a.bills))
	for i, b := range a.bills {
		out[i] = *b
	}
	return out, nil
}

// Repayments returns a copy of all repayment allocation records,
// oldest first.
func (l *Ledger) Repayments(id string) ([]Repayment, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	out := make([]Repayment, len(a.repayments))
	for i, r := range a.repayments {
		out[i] = *r
	}
	return out, nil
}

// AccrualSteps returns the number of lazy interest-accrual updates
// performed so far. It never exceeds the number of accepted operations,
// which is the machine-checkable evidence that billing cost depends only
// on the current period's transactions, not on history length.
func (l *Ledger) AccrualSteps(id string) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.accounts[id]
	if !ok {
		return 0, ErrAccountNotFound
	}
	return a.accrualSteps, nil
}
