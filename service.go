package mileage

import "sync"

type Service struct {
	mu       sync.Mutex
	config   Config
	accounts map[string]*account
}

func NewService(config Config) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{config: config, accounts: make(map[string]*account)}, nil
}

func validID(id string) bool { return id != "" }

func (s *Service) CreateAccount(id string, now int64) error {
	if !validID(id) || now < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.accounts[id]; exists {
		return ErrInvalidArgument
	}
	s.accounts[id] = newAccount(id, now)
	return nil
}

func (s *Service) Credit(in CreditInput) (CreditResult, error) {
	segment := in.Segment
	if !validID(in.AccountID) || in.Now < 0 || !validID(segment.ID) ||
		segment.Distance <= 0 || segment.FarePercent < 0 || segment.FarePercent > 300 ||
		segment.FlownAt < 0 {
		return CreditResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.Now < s.lastClockAt(in.AccountID) {
		return CreditResult{}, ErrClockRewind
	}
	acc, exists := s.accounts[in.AccountID]
	if !exists {
		return CreditResult{}, ErrAccountNotFound
	}
	if acc.frozenAt(s.config, in.Now) {
		return CreditResult{}, ErrAccountFrozen
	}
	if _, duplicate := acc.segments[segment.ID]; duplicate {
		return CreditResult{}, ErrDuplicateCredit
	}
	if in.Now > segment.FlownAt+s.config.RetroWindowSeconds {
		return CreditResult{}, ErrLateCredit
	}
	period, ok := acc.periodAt(s.config, segment.FlownAt)
	if !ok {
		return CreditResult{}, ErrInvalidArgument
	}

	acc.rollForward(s.config, in.Now)
	return acc.credit(s.config, in, period), nil
}

func (s *Service) Refund(in RefundInput) (RefundResult, error) {
	if !validID(in.AccountID) || in.Now < 0 || !validID(in.SegmentID) {
		return RefundResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.Now < s.lastClockAt(in.AccountID) {
		return RefundResult{}, ErrClockRewind
	}
	acc, exists := s.accounts[in.AccountID]
	if !exists {
		return RefundResult{}, ErrAccountNotFound
	}
	acc.rollForward(s.config, in.Now)
	return acc.refund(s.config, in), nil
}

func (s *Service) Redeem(in RedeemInput) (RedeemResult, error) {
	if !validID(in.AccountID) || in.Now < 0 || !validID(in.RedemptionID) ||
		in.CostMiles <= 0 {
		return RedeemResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.Now < s.lastClockAt(in.AccountID) {
		return RedeemResult{}, ErrClockRewind
	}
	acc, exists := s.accounts[in.AccountID]
	if !exists {
		return RedeemResult{}, ErrAccountNotFound
	}
	if acc.frozenAt(s.config, in.Now) {
		return RedeemResult{}, ErrAccountFrozen
	}
	if _, duplicate := acc.redemptions[in.RedemptionID]; duplicate {
		return RedeemResult{}, ErrInvalidArgument
	}
	if acc.redeemable < in.CostMiles {
		return RedeemResult{}, ErrInsufficientMiles
	}

	acc.rollForward(s.config, in.Now)
	return acc.redeem(s.config, in)
}

func (s *Service) CancelRedemption(in CancelRedemptionInput) (CancelRedemptionResult, error) {
	if !validID(in.AccountID) || in.Now < 0 || !validID(in.RedemptionID) {
		return CancelRedemptionResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.Now < s.lastClockAt(in.AccountID) {
		return CancelRedemptionResult{}, ErrClockRewind
	}
	acc, exists := s.accounts[in.AccountID]
	if !exists {
		return CancelRedemptionResult{}, ErrAccountNotFound
	}
	if acc.frozenAt(s.config, in.Now) {
		return CancelRedemptionResult{}, ErrAccountFrozen
	}
	record, found := acc.redemptions[in.RedemptionID]
	if !found {
		return CancelRedemptionResult{}, ErrRedemptionNotFound
	}
	if record.canceled {
		return CancelRedemptionResult{}, ErrRedemptionCanceled
	}

	acc.rollForward(s.config, in.Now)
	return acc.cancelRedemption(s.config, in)
}

func (s *Service) Unfreeze(in UnfreezeInput) error {
	if !validID(in.AccountID) || in.Now < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.Now < s.lastClockAt(in.AccountID) {
		return ErrClockRewind
	}
	acc, exists := s.accounts[in.AccountID]
	if !exists {
		return ErrAccountNotFound
	}
	acc.rollForward(s.config, in.Now)
	acc.unfreeze(s.config, in)
	return nil
}

func (s *Service) View(id string) (AccountView, error) {
	if !validID(id) {
		return AccountView{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, exists := s.accounts[id]
	if !exists {
		return AccountView{}, ErrAccountNotFound
	}
	return acc.view(), nil
}

func (s *Service) ViewAt(id string, now int64) (AccountView, error) {
	if !validID(id) || now < 0 {
		return AccountView{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, exists := s.accounts[id]
	if !exists {
		return AccountView{}, ErrAccountNotFound
	}
	if now < acc.lastClockAt {
		return AccountView{}, ErrClockRewind
	}
	return acc.viewAt(s.config, now), nil
}

func (s *Service) lastClockAt(id string) int64 {
	if acc, exists := s.accounts[id]; exists {
		return acc.lastClockAt
	}
	return 0
}
