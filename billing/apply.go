package billing

// Application records how much of a payment was applied to one bill.
type Application struct {
	BillID      string
	ToLate      int64
	ToPrincipal int64
}

// PaymentResult is the deterministic breakdown of an accepted payment.
type PaymentResult struct {
	Applications []Application
	Prepaid      int64 // leftover added to the household prepay balance
}

// applyToBill applies up to amount to a single bill, late fees first, then
// principal. Stage-3 bills are all-or-nothing: they are settled only when
// amount covers the full outstanding balance, otherwise they are skipped
// (which is not an error). Returns the applied amount and its breakdown.
func (s *Service) applyToBill(b *Bill, amount int64) (int64, Application) {
	app := Application{BillID: b.ID}
	outstanding := b.unpaidLate(s.cfg.den()) + b.unpaidPrincipal()
	if outstanding == 0 {
		return 0, app
	}
	pay := amount
	if b.Stage == 3 {
		if amount < outstanding {
			return 0, app
		}
		pay = outstanding
	}
	if pay > outstanding {
		pay = outstanding
	}
	app.ToLate = min(pay, b.unpaidLate(s.cfg.den()))
	app.ToPrincipal = pay - app.ToLate
	b.PaidLate += app.ToLate
	b.PaidPrincipal += app.ToPrincipal
	s.closeIfSettled(b)
	return pay, app
}

// closeIfSettled closes a fully settled bill, freezing its stage.
func (s *Service) closeIfSettled(b *Bill) {
	if !b.Closed && b.unpaidPrincipal() == 0 && b.unpaidLate(s.cfg.den()) == 0 {
		b.Closed = true
	}
}

// applyPayment sweeps the household's open bills in (due day, creation)
// order and applies amount, returning the breakdown and the leftover.
func (s *Service) applyPayment(h *Household, now int, amount int64) PaymentResult {
	res := PaymentResult{}
	for _, b := range h.open {
		b.accrueTo(now, s.cfg)
		b.Stage = b.stageAt(now, s.cfg.Thresholds)
	}
	remaining := amount
	for _, b := range h.open {
		if remaining == 0 {
			break
		}
		if b.Closed || b.Disputed {
			continue
		}
		applied, app := s.applyToBill(b, remaining)
		if applied > 0 {
			res.Applications = append(res.Applications, app)
			remaining -= applied
		}
	}
	h.compactOpen()
	res.Prepaid = remaining
	return res
}
