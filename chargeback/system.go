package chargeback

import "sync"

// System is the chargeback case-management and merchant claw-back engine.
//
// # Time semantics
//
// Accepted operations are the only stored mutation. A case's phase is a pure
// function of its operation history and the observed time (caseRec.derive).
// Immediate claw-backs/refunds/awards/fees from accepted operations post to
// an immutable day-coordinated timeline. Automatic deadline determinations
// never post to that timeline; instead their money effect at read time t is
// summed virtually by autoView. Therefore:
//
//   - rejected operations change nothing (no provisional state exists),
//   - a far-future query cannot change an earlier-time answer,
//   - the answer at any t depends only on accepted ops and t.
type System struct {
	mu     sync.Mutex
	cfg    Config
	reg    *registry
	tl     *timeline
	cases  map[string]*caseRec
	lastOp int

	// Aggregates over cases that have no explicit terminating operation.
	// Automatic determinations only move money between issuer/pending/merchant
	// within this set; the values at now are derived by scanning the set's
	// cases. The set is indexed per merchant below for the merchant query.
}

// New constructs a System with the given configuration.
func New(cfg Config) *System {
	return &System{
		cfg:   cfg,
		reg:   newRegistry(),
		tl:    newTimeline(),
		cases: map[string]*caseRec{},
	}
}

// autoMerchantDelta is the automatic refund to a merchant at now from cases
// with no explicit terminating operation. It scans only that merchant's
// cases, so its cost is independent of global history.
func (s *System) autoMerchantDelta(merchantID string, now int) int64 {
	var sum int64
	for _, id := range s.reg.openCasesOfMerchant(merchantID) {
		c := s.cases[id]
		if c.term != nil {
			continue
		}
		if p, _ := c.derive(s.cfg, now); p == phMerchantWin {
			sum += c.amount
		}
	}
	return sum
}

// autoIssuerAndPending derives automatic issuer winnings and their release of
// pending holds at now. It scans only cases without an explicit terminating
// operation (the open list), which is bounded by active lifecycle work, not by
// total closed history.
func (s *System) autoIssuerAndPending(now int) (issuer, pendingAdj int64) {
	for _, id := range s.reg.openList() {
		c := s.cases[id]
		if c.term != nil {
			continue
		}
		switch p, _ := c.derive(s.cfg, now); p {
		case phIssuerWin:
			issuer += c.amount
			pendingAdj -= c.amount
		case phMerchantWin:
			pendingAdj -= c.amount
		}
	}
	return issuer, pendingAdj
}

func (s *System) window(r Reason) int {
	switch r {
	case ReasonFraud:
		return s.cfg.FraudWindowDays
	case ReasonNotReceived:
		return s.cfg.NotReceivedWindowDays
	default:
		return s.cfg.DuplicateWindowDays
	}
}

func (s *System) liveAt(c *caseRec, now int) bool {
	p, _ := c.derive(s.cfg, now)
	return p != phMerchantWin
}

func (s *System) disputableAt(txnID string, now int) int64 {
	t := s.reg.get(txnID)
	var occ int64
	for _, id := range s.reg.casesOf(txnID) {
		if s.liveAt(s.cases[id], now) {
			occ += s.cases[id].amount
		}
	}
	return t.Amount - occ
}

func (s *System) merchantWinOnReason(txnID string, reason Reason, now int) bool {
	for _, id := range s.reg.casesOf(txnID) {
		c := s.cases[id]
		if c.reason == reason {
			if p, _ := c.derive(s.cfg, now); p == phMerchantWin {
				return true
			}
		}
	}
	return false
}

func (s *System) basisLocked(basisTxnID string, now int) bool {
	for _, id := range s.reg.casesOnBasis(basisTxnID) {
		if p, _ := s.cases[id].derive(s.cfg, now); p != phMerchantWin {
			return true
		}
	}
	return false
}

func (s *System) RegisterTransaction(now int, txn Transaction) error {
	if txn.ID == "" || txn.CardID == "" || txn.MerchantID == "" ||
		txn.Amount <= 0 || txn.SettlementDay < 0 || now < txn.SettlementDay {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	if _, exists := s.reg.txn[txn.ID]; exists {
		return ErrInvalidArgument
	}
	t := txn
	s.reg.add(&t)
	s.lastOp = now
	return nil
}

func (s *System) CreditMerchant(now int, merchantID string, amount int64) error {
	if merchantID == "" || amount <= 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	s.tl.creditMerchant(merchantID, now, amount)
	s.lastOp = now
	return nil
}

func (s *System) File(now int, txnID string, reason Reason, amount int64, caseID string) error {
	if txnID == "" || caseID == "" || amount <= 0 ||
		reason < ReasonFraud || reason > ReasonDuplicate {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	txn := s.reg.get(txnID)
	if txn == nil {
		return ErrTransactionNotFound
	}
	if now-txn.SettlementDay > s.window(reason) {
		return ErrWindowExpired
	}
	if s.merchantWinOnReason(txnID, reason, now) {
		return ErrDuplicateFiling
	}
	if _, dup := s.cases[caseID]; dup {
		return ErrInvalidArgument
	}
	if amount > s.disputableAt(txnID, now) {
		return ErrAmountExceeded
	}
	var basis *Transaction
	if reason == ReasonDuplicate {
		basis = s.reg.findBasis(txn, s.cfg.DuplicateRangeDays)
		if basis == nil || s.basisLocked(basis.ID, now) {
			return ErrNoBasis
		}
	}
	c := &caseRec{
		id: caseID, txnID: txnID, merchantID: txn.MerchantID,
		reason: reason, amount: amount, fileDay: now, defenseDay: -1,
	}
	if basis != nil {
		c.basisTxnID = basis.ID
	}
	s.tl.postMerchant(txn.MerchantID, now, -amount)
	s.tl.postPending(now, amount)
	s.cases[caseID] = c
	s.reg.addCase(txnID, txn.MerchantID, c.basisTxnID, caseID)
	s.reg.addOpen(caseID)
	s.reg.addMerchantOpen(txn.MerchantID, caseID)
	s.lastOp = now
	return nil
}

func (s *System) Defend(now int, caseID string) error {
	if caseID == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	c := s.cases[caseID]
	if c == nil {
		return ErrCaseNotFound
	}
	if p, _ := c.derive(s.cfg, now); p != phDefense {
		return ErrStatusNotAllowed
	}
	c.defenseDay = now
	s.lastOp = now
	return nil
}

func (s *System) AcceptDefense(now int, caseID string) error {
	if caseID == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	c := s.cases[caseID]
	if c == nil {
		return ErrCaseNotFound
	}
	if p, _ := c.derive(s.cfg, now); p != phReview {
		return ErrStatusNotAllowed
	}
	c.term = &manualTerm{day: now, outcome: OutcomeMerchantWin}
	s.reg.removeOpen(caseID)
	s.reg.removeMerchantOpen(c.merchantID, caseID)
	s.tl.postMerchant(c.merchantID, now, c.amount)
	s.tl.postPending(now, -c.amount)
	s.lastOp = now
	return nil
}

func (s *System) PreArbitration(now int, caseID string) error {
	if caseID == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	c := s.cases[caseID]
	if c == nil {
		return ErrCaseNotFound
	}
	if p, _ := c.derive(s.cfg, now); p != phReview {
		return ErrStatusNotAllowed
	}
	c.preArb = true
	s.lastOp = now
	return nil
}

func (s *System) Rule(now int, caseID string, winner Outcome) error {
	if caseID == "" || (winner != OutcomeMerchantWin && winner != OutcomeIssuerWin) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return ErrClockRollback
	}
	c := s.cases[caseID]
	if c == nil {
		return ErrCaseNotFound
	}
	if p, _ := c.derive(s.cfg, now); p != phArb {
		return ErrStatusNotAllowed
	}
	c.term = &manualTerm{day: now, outcome: winner}
	s.reg.removeOpen(caseID)
	s.reg.removeMerchantOpen(c.merchantID, caseID)
	if winner == OutcomeMerchantWin {
		s.tl.postMerchant(c.merchantID, now, c.amount)
		s.tl.postPending(now, -c.amount)
		s.tl.postIssuer(now, -s.cfg.ArbitrationFee)
	} else {
		s.tl.postIssuer(now, c.amount)
		s.tl.postPending(now, -c.amount)
		s.tl.postMerchant(c.merchantID, now, -s.cfg.ArbitrationFee)
	}
	s.lastOp = now
	return nil
}

func (s *System) MerchantBalance(now int, merchantID string) (int64, error) {
	if merchantID == "" {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return 0, ErrClockRollback
	}
	return s.tl.merchantBalance(merchantID, now) + s.autoMerchantDelta(merchantID, now), nil
}

func (s *System) IssuerAccount(now int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return 0, ErrClockRollback
	}
	iss, _ := s.autoIssuerAndPending(now)
	return s.tl.issuerAt(now) + iss, nil
}

func (s *System) PendingHeld(now int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return 0, ErrClockRollback
	}
	_, padj := s.autoIssuerAndPending(now)
	return s.tl.pendingAt(now) + padj, nil
}

func (s *System) Disputable(now int, txnID string) (int64, error) {
	if txnID == "" {
		return 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return 0, ErrClockRollback
	}
	txn := s.reg.get(txnID)
	if txn == nil {
		return 0, ErrTransactionNotFound
	}
	return s.disputableAt(txnID, now), nil
}

func (s *System) CaseStatus(now int, caseID string) (Status, Outcome, error) {
	if caseID == "" {
		return 0, 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastOp {
		return 0, 0, ErrClockRollback
	}
	c := s.cases[caseID]
	if c == nil {
		return 0, 0, ErrCaseNotFound
	}
	p, _ := c.derive(s.cfg, now)
	return phaseStatus(p), phaseOutcome(p), nil
}
