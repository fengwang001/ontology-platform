package settlement

import "sync"

// System is the concurrency-safe entry point. All accepted operations carry a
// monotonically non-decreasing now; a rejected operation leaves every state,
// including the shared clock, untouched.
//
// Error priority for transactions:
//
//	invalid argument > clock rollback > merchant missing > duplicate id >
//	invalid date (tx day after now) > book sealed (tx day before frontier).
//
// Error priority for settlement:
//
//	invalid argument > clock rollback > merchant missing > non-business day >
//	duplicate settlement.
type System struct {
	mu        sync.Mutex
	cal       *calendar
	lastNow   Day
	haveNow   bool
	merchants map[string]*merchant
}

// NewSystem creates an empty system whose business days are the given set.
// Days may be supplied in any order and are de-duplicated; at least one day is
// required.
func NewSystem(businessDays []Day) (*System, error) {
	if len(businessDays) == 0 {
		return nil, ErrInvalidArgument
	}
	seen := make(map[Day]struct{}, len(businessDays))
	uniq := make([]Day, 0, len(businessDays))
	for _, d := range businessDays {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		uniq = append(uniq, d)
	}
	return &System{
		cal:       newCalendar(uniq),
		merchants: make(map[string]*merchant),
	}, nil
}

func validConfig(cfg MerchantConfig) bool {
	if cfg.SettleDelayN < 1 || cfg.ReserveHorizonH < 1 {
		return false
	}
	if cfg.ReserveBps < 0 || cfg.ReserveBps > 10000 {
		return false
	}
	return true
}

// AddMerchant registers a merchant. It obeys the shared clock like every other
// mutating operation.
func (s *System) AddMerchant(now Day, id string, cfg MerchantConfig) error {
	if id == "" || !validConfig(cfg) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.haveNow && now < s.lastNow {
		return ErrClockRolledBack
	}
	if _, ok := s.merchants[id]; ok {
		return ErrInvalidArgument
	}
	s.merchants[id] = newMerchant(id, cfg, s.cal)
	s.lastNow = now
	s.haveNow = true
	return nil
}

// AddTransaction appends a signed ledger entry to a merchant.
func (s *System) AddTransaction(now Day, merchantID string, tx Transaction) error {
	if merchantID == "" || tx.ID == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.haveNow && now < s.lastNow {
		return ErrClockRolledBack
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return ErrMerchantMissing
	}
	if m.hasTxn(tx.ID) {
		return ErrDuplicateTxnID
	}
	if tx.Day > now {
		return ErrInvalidDate
	}
	if last, ok := m.lastSettledDay(); ok && tx.Day < last {
		return ErrBookSealed
	}

	m.addTransaction(tx)
	s.lastNow = now
	return nil
}

// Settle processes a merchant's outstanding business days through d inclusive
// and returns one Payout per business day processed.
func (s *System) Settle(now Day, merchantID string, d Day) ([]Payout, []DayDetail, error) {
	if merchantID == "" {
		return nil, nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.haveNow && now < s.lastNow {
		return nil, nil, ErrClockRolledBack
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return nil, nil, ErrMerchantMissing
	}
	if !s.cal.contains(d) {
		return nil, nil, ErrNonBusinessDay
	}
	if last, ok := m.lastSettledDay(); ok && d <= last {
		return nil, nil, ErrDuplicateSettle
	}

	details := m.settleThrough(d)
	payouts := make([]Payout, len(details))
	for i, dt := range details {
		payouts[i] = Payout{MerchantID: merchantID, Day: dt.Day, Amount: dt.Payout}
	}
	s.lastNow = now
	return payouts, details, nil
}

// State returns a deep-copied snapshot of one merchant.
func (s *System) State(merchantID string) (MerchantState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.merchants[merchantID]
	if !ok {
		return MerchantState{}, ErrMerchantMissing
	}
	return m.state(), nil
}

// Clock returns the most recently accepted now.
func (s *System) Clock() (Day, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastNow, s.haveNow
}
