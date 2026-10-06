package billing

// Dispute opens a dispute on a bill. The late fee is accrued through
// the day before the dispute and then frozen; the dunning stage is
// frozen and payments skip the bill until the dispute is resolved. A
// bill can be disputed only once.
func (s *Service) Dispute(householdID, billID string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" {
		return paramErr("household id and bill id must be non-empty")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return herr
	}
	b, berr := h.bill(billID)
	if berr != nil {
		return berr
	}
	if b.closed {
		return stateErr("bill %q is closed", billID)
	}
	if b.everDisputed {
		return stateErr("bill %q was already disputed once", billID)
	}
	s.lastNow = now
	b.accrueTo(s.cfg, now-1) // the dispute day itself does not accrue
	b.disputed = true
	b.disputeStart = now
	b.everDisputed = true
	return nil
}

// ResolveDispute rules on an open dispute. With reduce=false the
// principal is upheld; with reduce=true the principal is lowered to
// newPrincipal, which becomes the new basis for accrual and the cap.
// Payments already made are not refunded: if the paid principal exceeds
// the reduced principal, the excess moves to the prepayment balance.
// Disputed days are not back-counted; accrual resumes on the ruling day.
func (s *Service) ResolveDispute(householdID, billID string, now int64, reduce bool, newPrincipal int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" {
		return paramErr("household id and bill id must be non-empty")
	}
	if reduce && newPrincipal < 0 {
		return paramErr("reduced principal must be >= 0, got %d", newPrincipal)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return herr
	}
	b, berr := h.bill(billID)
	if berr != nil {
		return berr
	}
	if reduce && newPrincipal >= b.principal {
		return paramErr("reduced principal %d is not below current principal %d", newPrincipal, b.principal)
	}
	if !b.disputed {
		return stateErr("bill %q has no open dispute", billID)
	}
	s.lastNow = now
	b.disputed = false
	b.ruled = true
	b.disputedDays += now - b.disputeStart
	if reduce {
		b.principal = newPrincipal
		if b.principalPaid > b.principal {
			excess := b.principalPaid - b.principal
			b.principalPaid = b.principal
			h.allocPrincipal -= excess
			h.prepay += excess // not refunded, kept as prepayment
		}
	}
	if r := now - 1; r > b.accruedThrough {
		b.accruedThrough = r // accrual resumes on the ruling day
	}
	b.advanceStage(s.cfg, now)
	h.maybeClose(b)
	return nil
}

// Waive forgives part of the accrued late fee of a bill. The amount may
// not exceed the unpaid late fee as of now. Waiving does not affect the
// dunning stage, and the remaining unpaid principal keeps accruing new
// late fees afterwards.
func (s *Service) Waive(householdID, billID string, amount, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" {
		return paramErr("household id and bill id must be non-empty")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return herr
	}
	b, berr := h.bill(billID)
	if berr != nil {
		return berr
	}
	if b.closed {
		return stateErr("bill %q is closed", billID)
	}
	unpaid := b.projectedLateFee(s.cfg, now) - b.lateFeePaid - b.lateFeeWaived
	if amount <= 0 || amount > unpaid {
		return amountErr("waiver %d out of range (1..%d)", amount, unpaid)
	}
	s.lastNow = now
	b.accrueTo(s.cfg, now)
	b.lateFeeWaived += amount
	b.advanceStage(s.cfg, now)
	h.maybeClose(b)
	return nil
}
