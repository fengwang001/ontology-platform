package ontology

type LedgerEntryKind string

const (
	LedgerTopUp   LedgerEntryKind = "top_up"
	LedgerPenalty LedgerEntryKind = "penalty"
	LedgerRefund  LedgerEntryKind = "refund"
)

type LedgerEntry struct {
	At      int64
	Kind    LedgerEntryKind
	Amount  int64
	Balance int64
	Ref     string
	Reason  string
}

func (s *Service) TopUp(now int64, household string, amount int64) error {
	if amount <= 0 {
		return illegal("top-up amount must be positive")
	}
	if !nonEmptyID(household) {
		return illegal("household id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.households[household]; !ok {
		return notFound("household does not exist")
	}
	s.advanceClock(now)
	s.appendLedger(household, LedgerEntry{At: now, Kind: LedgerTopUp, Amount: amount, Ref: "top-up", Reason: "deposit top-up"})
	return nil
}

func (s *Service) Balance(now int64, household string) (int64, error) {
	if !nonEmptyID(household) {
		return 0, illegal("household id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	if _, ok := s.households[household]; !ok {
		return 0, notFound("household does not exist")
	}
	s.advanceClock(now)
	return s.cashBalance(household), nil
}

func (s *Service) Ledger(now int64, household string) ([]LedgerEntry, error) {
	if !nonEmptyID(household) {
		return nil, illegal("household id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if _, ok := s.households[household]; !ok {
		return nil, notFound("household does not exist")
	}
	s.advanceClock(now)
	return append([]LedgerEntry(nil), s.ledgers[household]...), nil
}

func (s *Service) cashBalance(household string) int64 {
	entries := s.ledgers[household]
	if len(entries) == 0 {
		return 0
	}
	return entries[len(entries)-1].Balance
}

func (s *Service) availableBalance(household string) int64 {
	return s.cashBalance(household) - int64(s.holds[household])*s.cfg.Penalty
}

func (s *Service) addHold(household string) { s.holds[household]++ }

func (s *Service) releaseHold(household string) {
	if s.holds[household] > 0 {
		s.holds[household]--
	}
}

func (s *Service) chargePenalty(household, ref string, now int64) {
	s.releaseHold(household)
	s.appendLedger(household, LedgerEntry{At: now, Kind: LedgerPenalty, Amount: -s.cfg.Penalty, Ref: ref, Reason: "contract penalty"})
}

func (s *Service) refund(household, ref string, now int64, amount int64) {
	s.appendLedger(household, LedgerEntry{At: now, Kind: LedgerRefund, Amount: -amount, Ref: ref, Reason: "deposit refund"})
}

func (s *Service) appendLedger(household string, entry LedgerEntry) {
	entry.Balance = s.cashBalance(household) + entry.Amount
	s.ledgers[household] = append(s.ledgers[household], entry)
}
