package naive

func (m *Model) RegisterTransaction(now int, t Transaction) error {
	if t.ID == "" || t.CardID == "" || t.MerchantID == "" ||
		t.Amount <= 0 || t.SettlementDay < 0 || now < t.SettlementDay {
		return ErrInvalidArgument
	}
	if now < m.last {
		return ErrClockRollback
	}
	if m.txnIDs[t.ID] {
		return ErrInvalidArgument
	}
	m.ops = append(m.ops, op{
		kind: opRegister, now: now, txnID: t.ID, card: t.CardID,
		merch: t.MerchantID, amount: t.Amount, settle: t.SettlementDay,
	})
	m.txnIDs[t.ID] = true
	m.last = now
	return nil
}

func (m *Model) CreditMerchant(now int, merchantID string, amount int64) error {
	if merchantID == "" || amount <= 0 {
		return ErrInvalidArgument
	}
	if now < m.last {
		return ErrClockRollback
	}
	m.ops = append(m.ops, op{kind: opCredit, now: now, merch: merchantID, amount: amount})
	m.last = now
	return nil
}

func (m *Model) window(r Reason) int {
	switch r {
	case ReasonFraud:
		return m.cfg.FraudWindowDays
	case ReasonNotReceived:
		return m.cfg.NotReceivedWindowDays
	default:
		return m.cfg.DuplicateWindowDays
	}
}

func (m *Model) File(now int, txnID string, reason Reason, amount int64, caseID string) error {
	if txnID == "" || caseID == "" || amount <= 0 ||
		reason < ReasonFraud || reason > ReasonDuplicate {
		return ErrInvalidArgument
	}
	if now < m.last {
		return ErrClockRollback
	}
	w := m.replayForDecision(now)
	t := w.txn[txnID]
	if t == nil {
		return ErrTransactionNotFound
	}
	if now-t.SettlementDay > m.window(reason) {
		return ErrWindowExpired
	}
	if w.merchantWinOnReason(txnID, reason) {
		return ErrDuplicateFiling
	}
	if _, exists := w.cases[caseID]; exists {
		return ErrInvalidArgument
	}
	if amount > t.Amount-w.liveOccupancy(txnID) {
		return ErrAmountExceeded
	}
	var basis string
	if reason == ReasonDuplicate {
		b := w.findBasis(t, m.cfg.DuplicateRangeDays)
		if b == nil {
			return ErrNoBasis
		}
		if w.basisLocked(b.ID, "") {
			return ErrNoBasis
		}
		basis = b.ID
	}
	m.ops = append(m.ops, op{
		kind: opFile, now: now, txnID: txnID, reason: reason,
		amount: amount, caseID: caseID, basis: basis,
	})
	m.last = now
	return nil
}

func (m *Model) genericCaseOp(now int, caseID string, kind opKind) error {
	if caseID == "" {
		return ErrInvalidArgument
	}
	if now < m.last {
		return ErrClockRollback
	}
	w := m.replayForDecision(now)
	c := w.cases[caseID]
	if c == nil {
		return ErrCaseNotFound
	}
	allowed := false
	switch kind {
	case opDefend:
		allowed = c.status == StatusAwaitingDefense
	case opAccept, opPreArb:
		allowed = c.status == StatusAwaitingReview
	case opRule:
		allowed = c.status == StatusPreArbitration
	}
	if !allowed {
		return ErrStatusNotAllowed
	}
	m.ops = append(m.ops, op{kind: kind, now: now, caseID: caseID})
	m.last = now
	return nil
}

func (m *Model) Defend(now int, caseID string) error {
	return m.genericCaseOp(now, caseID, opDefend)
}

func (m *Model) AcceptDefense(now int, caseID string) error {
	return m.genericCaseOp(now, caseID, opAccept)
}

func (m *Model) PreArbitration(now int, caseID string) error {
	return m.genericCaseOp(now, caseID, opPreArb)
}

func (m *Model) Rule(now int, caseID string, winner Outcome) error {
	if caseID == "" || (winner != OutcomeMerchantWin && winner != OutcomeIssuerWin) {
		return ErrInvalidArgument
	}
	if now < m.last {
		return ErrClockRollback
	}
	w := m.replayForDecision(now)
	c := w.cases[caseID]
	if c == nil {
		return ErrCaseNotFound
	}
	if c.status != StatusPreArbitration {
		return ErrStatusNotAllowed
	}
	m.ops = append(m.ops, op{kind: opRule, now: now, caseID: caseID, winner: winner})
	m.last = now
	return nil
}
