package billing

// Allocation records how much of one payment offset a single bill.
type Allocation struct {
	BillID    string
	LateFee   int64
	Principal int64
}

// Receipt is the allocation detail of an accepted payment.
type Receipt struct {
	Amount      int64
	Allocations []Allocation
	PrepayAdded int64 // leftover that became prepayment balance
}

// Pay accepts a (possibly partial) payment for a household. The late
// fees of all open bills are first accrued through the payment day,
// then the payment offsets bills in (dueDay, seq) order: within a bill
// the late fee first, then the principal. Disputed bills are skipped.
// Stage-3 bills are only offset when the remaining payment covers the
// bill in full; otherwise they are skipped without error. Any leftover
// becomes prepayment balance.
func (s *Service) Pay(householdID string, amount, now int64) (*Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" {
		return nil, paramErr("household id is empty")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return nil, herr
	}
	if amount <= 0 {
		return nil, amountErr("payment amount must be positive, got %d", amount)
	}
	s.lastNow = now

	for _, b := range h.open {
		b.accrueTo(s.cfg, now)
		b.advanceStage(s.cfg, now)
	}

	remaining := amount
	receipt := &Receipt{Amount: amount}
	stillOpen := h.open[:0]
	for _, b := range h.open {
		if remaining == 0 || b.disputed {
			stillOpen = append(stillOpen, b)
			continue
		}
		need := b.unpaidLateFee() + b.unpaidPrincipal()
		if b.stage == StageFinal && remaining < need {
			stillOpen = append(stillOpen, b) // full-payment-only: skip
			continue
		}
		alloc := Allocation{BillID: b.id}
		if x := min(remaining, b.unpaidLateFee()); x > 0 {
			b.lateFeePaid += x
			alloc.LateFee = x
			remaining -= x
		}
		if x := min(remaining, b.unpaidPrincipal()); x > 0 {
			b.principalPaid += x
			alloc.Principal = x
			remaining -= x
		}
		h.allocLateFee += alloc.LateFee
		h.allocPrincipal += alloc.Principal
		receipt.Allocations = append(receipt.Allocations, alloc)
		if b.unpaidPrincipal() == 0 && b.unpaidLateFee() == 0 {
			b.closed = true
			h.closed = append(h.closed, b)
		} else {
			stillOpen = append(stillOpen, b)
		}
	}
	h.open = stillOpen
	if remaining > 0 {
		h.prepay += remaining
		receipt.PrepayAdded = remaining
	}
	h.totalPaid += amount
	return receipt, nil
}
