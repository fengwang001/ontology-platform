package billing

// BillView is a read-only snapshot of a bill.
type BillView struct {
	ID            string
	Period        string
	DueDay        int64
	Principal     int64
	PrincipalPaid int64
	LateFee       int64
	LateFeePaid   int64
	LateFeeWaived int64
	Stage         int
	Closed        bool
	Disputed      bool
	Ruled         bool
	DisputedDays  int64
}

func viewOf(b *Bill) BillView {
	return BillView{
		ID:            b.id,
		Period:        b.period,
		DueDay:        b.dueDay,
		Principal:     b.principal,
		PrincipalPaid: b.principalPaid,
		LateFee:       b.lateFee,
		LateFeePaid:   b.lateFeePaid,
		LateFeeWaived: b.lateFeeWaived,
		Stage:         b.stage,
		Closed:        b.closed,
		Disputed:      b.disputed,
		Ruled:         b.ruled,
		DisputedDays:  b.disputedDays,
	}
}

// HouseholdView is a read-only snapshot of a household ledger. The
// conservation invariant is TotalPaid == AllocatedPrincipal +
// AllocatedLateFee + Prepay.
type HouseholdView struct {
	ID                 string
	Prepay             int64
	TotalPaid          int64
	AllocatedPrincipal int64
	AllocatedLateFee   int64
	OpenBills          int
	ClosedBills        int
}

// TotalDue returns the amount the household would owe on the given day:
// unpaid principal plus the late fee projected through that day, over
// the open bills only. It is a pure query: nothing is mutated and the
// clock is untouched. Its cost is proportional to the number of open
// bills, never to the historical bill count (see QueryVisits).
func (s *Service) TotalDue(householdID string, day int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" {
		return 0, paramErr("household id is empty")
	}
	if day < 0 {
		return 0, paramErr("day must be >= 0, got %d", day)
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return 0, herr
	}
	if day < s.lastNow {
		return 0, paramErr("query day %d is before current now %d", day, s.lastNow)
	}
	s.queryVisits = 0
	var total int64
	for _, b := range h.open {
		s.queryVisits++
		total += b.unpaidPrincipal()
		total += b.projectedLateFee(s.cfg, day) - b.lateFeePaid - b.lateFeeWaived
	}
	return total, nil
}

// QueryVisits returns how many bills the last TotalDue call visited.
// It always equals the household's open-bill count, which proves the
// query cost is independent of the historical bill count.
func (s *Service) QueryVisits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryVisits
}

// BillView returns a snapshot of one bill.
func (s *Service) BillView(householdID, billID string) (BillView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" {
		return BillView{}, paramErr("household id and bill id must be non-empty")
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return BillView{}, herr
	}
	b, berr := h.bill(billID)
	if berr != nil {
		return BillView{}, berr
	}
	return viewOf(b), nil
}

// HouseholdView returns a snapshot of a household ledger.
func (s *Service) HouseholdView(householdID string) (HouseholdView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" {
		return HouseholdView{}, paramErr("household id is empty")
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return HouseholdView{}, herr
	}
	return HouseholdView{
		ID:                 h.id,
		Prepay:             h.prepay,
		TotalPaid:          h.totalPaid,
		AllocatedPrincipal: h.allocPrincipal,
		AllocatedLateFee:   h.allocLateFee,
		OpenBills:          len(h.open),
		ClosedBills:        len(h.closed),
	}, nil
}
