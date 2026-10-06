package billing

import (
	"sort"
	"sync"
)

// Household holds one household's ledger. open contains only unclosed bills
// sorted by (due day, creation order), so scans never touch closed history.
type Household struct {
	ID        string
	bills     map[string]*Bill
	open      []*Bill
	Prepay    int64
	TotalPaid int64
}

func (h *Household) compactOpen() {
	open := h.open[:0]
	for _, b := range h.open {
		if !b.Closed {
			open = append(open, b)
		}
	}
	h.open = open
}

// Service is the billing service. A single mutex serializes all operations,
// so concurrent calls are equivalent to some serial order.
type Service struct {
	mu         sync.Mutex
	cfg        Config
	lastNow    int
	seq        int
	households map[string]*Household

	scanCount int // bills visited by the last TotalDue call (test instrumentation)
}

func NewService(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, lastNow: -1, households: map[string]*Household{}}, nil
}

func (s *Service) checkClock(now int) error {
	if now < s.lastNow {
		return newError(ErrClockRollback, "now=%d is before last accepted now=%d", now, s.lastNow)
	}
	return nil
}

func (s *Service) getHousehold(id string) (*Household, error) {
	h, ok := s.households[id]
	if !ok {
		return nil, newError(ErrNotFound, "household %q does not exist", id)
	}
	return h, nil
}

func (s *Service) getBill(h *Household, billID string) (*Bill, error) {
	b, ok := h.bills[billID]
	if !ok {
		return nil, newError(ErrNotFound, "bill %q does not exist for household %q", billID, h.ID)
	}
	return b, nil
}

// GenerateBill creates a bill for a household (created on first use) and
// immediately applies any prepay balance to it.
func (s *Service) GenerateBill(now int, householdID, billID string, principal int64, dueDay int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" || principal <= 0 || dueDay < 0 || now < 0 {
		return newError(ErrInvalidParam, "bad arguments: household=%q bill=%q principal=%d dueDay=%d now=%d",
			householdID, billID, principal, dueDay, now)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h := s.households[householdID]
	if h == nil {
		h = &Household{ID: householdID, bills: map[string]*Bill{}}
		s.households[householdID] = h
	}
	if _, dup := h.bills[billID]; dup {
		return newError(ErrInvalidState, "bill %q already exists for household %q", billID, householdID)
	}
	b := &Bill{
		ID:             billID,
		DueDay:         dueDay,
		Principal:      principal,
		LastAccrualDay: dueDay + s.cfg.GraceDays,
		seq:            s.seq,
	}
	s.seq++
	b.accrueTo(now, s.cfg)
	b.Stage = b.stageAt(now, s.cfg.Thresholds)
	h.bills[billID] = b
	h.open = append(h.open, b)
	sort.SliceStable(h.open, func(i, j int) bool {
		if h.open[i].DueDay != h.open[j].DueDay {
			return h.open[i].DueDay < h.open[j].DueDay
		}
		return h.open[i].seq < h.open[j].seq
	})
	if h.Prepay > 0 && !b.Closed {
		applied, _ := s.applyToBill(b, h.Prepay)
		h.Prepay -= applied
	}
	h.compactOpen()
	s.lastNow = now
	return nil
}

// Pay applies a (possibly partial) payment: open bills in (due day, creation)
// order, late fees before principal within a bill, disputed bills skipped,
// stage-3 bills all-or-nothing. Leftover becomes prepay.
func (s *Service) Pay(now int, householdID string, amount int64) (PaymentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || amount < 0 || now < 0 {
		return PaymentResult{}, newError(ErrInvalidParam, "bad arguments: household=%q amount=%d now=%d",
			householdID, amount, now)
	}
	if err := s.checkClock(now); err != nil {
		return PaymentResult{}, err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return PaymentResult{}, err
	}
	if amount == 0 {
		return PaymentResult{}, newError(ErrAmount, "payment amount must be positive")
	}
	res := s.applyPayment(h, now, amount)
	h.Prepay += res.Prepaid
	h.TotalPaid += amount
	s.lastNow = now
	return res, nil
}

// Dispute opens a dispute on a bill: accrual stops, the dunning stage freezes
// and payments skip the bill. A bill can be disputed only once.
func (s *Service) Dispute(now int, householdID, billID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" || now < 0 {
		return newError(ErrInvalidParam, "bad arguments: household=%q bill=%q now=%d", householdID, billID, now)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return err
	}
	b, err := s.getBill(h, billID)
	if err != nil {
		return err
	}
	if b.Closed {
		return newError(ErrInvalidState, "bill %q is closed", billID)
	}
	if b.EverDisputed {
		return newError(ErrInvalidState, "bill %q was already disputed", billID)
	}
	b.accrueTo(now-1, s.cfg) // the dispute day itself does not accrue
	b.Disputed = true
	b.EverDisputed = true
	b.DisputeDay = now
	b.Stage = b.stageAt(now, s.cfg.Thresholds)
	s.lastNow = now
	return nil
}

// ResolveDispute rules on a disputed bill. newPrincipal must not exceed the
// current principal (pass it unchanged to uphold). Dispute days are not
// back-filled; accrual resumes from the ruling day. Past payments are not
// refunded.
func (s *Service) ResolveDispute(now int, householdID, billID string, newPrincipal int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" || newPrincipal < 0 || now < 0 {
		return newError(ErrInvalidParam, "bad arguments: household=%q bill=%q newPrincipal=%d now=%d",
			householdID, billID, newPrincipal, now)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return err
	}
	b, err := s.getBill(h, billID)
	if err != nil {
		return err
	}
	if newPrincipal > b.Principal {
		return newError(ErrInvalidParam, "new principal %d exceeds current principal %d", newPrincipal, b.Principal)
	}
	if !b.Disputed {
		return newError(ErrInvalidState, "bill %q is not under dispute", billID)
	}
	b.DisputedDays += now - b.DisputeDay
	b.Disputed = false
	b.Principal = newPrincipal
	b.LastAccrualDay = now - 1 // accrual resumes from the ruling day
	b.Stage = b.stageAt(now, s.cfg.Thresholds)
	s.closeIfSettled(b)
	h.compactOpen()
	s.lastNow = now
	return nil
}

// Waive forgives accrued late fees only, up to the unpaid late-fee amount.
// It never touches principal and does not affect the dunning stage.
func (s *Service) Waive(now int, householdID, billID string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" || amount < 0 || now < 0 {
		return newError(ErrInvalidParam, "bad arguments: household=%q bill=%q amount=%d now=%d",
			householdID, billID, amount, now)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return err
	}
	b, err := s.getBill(h, billID)
	if err != nil {
		return err
	}
	if b.Closed {
		return newError(ErrInvalidState, "bill %q is closed", billID)
	}
	if amount == 0 {
		return newError(ErrAmount, "waive amount must be positive")
	}
	unpaidLate := b.accruedUnitsAt(now, s.cfg)/s.cfg.den() - b.PaidLate - b.WaivedLate
	if amount > unpaidLate {
		return newError(ErrAmount, "waive amount %d exceeds unpaid late fee %d", amount, unpaidLate)
	}
	b.accrueTo(now, s.cfg)
	b.WaivedLate += amount
	s.closeIfSettled(b)
	h.compactOpen()
	s.lastNow = now
	return nil
}

// BillStatus is a read-only snapshot of one bill at day now.
type BillStatus struct {
	DueDay        int
	Principal     int64
	PaidPrincipal int64
	AccruedLate   int64
	PaidLate      int64
	WaivedLate    int64
	Stage         int
	Closed        bool
	Disputed      bool
	DisputedDays  int
}

func (s *Service) billStatus(b *Bill, now int) BillStatus {
	return BillStatus{
		DueDay:        b.DueDay,
		Principal:     b.Principal,
		PaidPrincipal: b.PaidPrincipal,
		AccruedLate:   b.accruedUnitsAt(now, s.cfg) / s.cfg.den(),
		PaidLate:      b.PaidLate,
		WaivedLate:    b.WaivedLate,
		Stage:         b.stageAt(now, s.cfg.Thresholds),
		Closed:        b.Closed,
		Disputed:      b.Disputed,
		DisputedDays:  b.DisputedDays,
	}
}

// Status returns a read-only snapshot of one bill at day now.
func (s *Service) Status(now int, householdID, billID string) (BillStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || billID == "" || now < 0 {
		return BillStatus{}, newError(ErrInvalidParam, "bad arguments: household=%q bill=%q now=%d",
			householdID, billID, now)
	}
	if err := s.checkClock(now); err != nil {
		return BillStatus{}, err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return BillStatus{}, err
	}
	b, err := s.getBill(h, billID)
	if err != nil {
		return BillStatus{}, err
	}
	return s.billStatus(b, now), nil
}

// HouseholdView is a read-only snapshot of a whole household at day now.
type HouseholdView struct {
	Prepay    int64
	TotalPaid int64
	Bills     map[string]BillStatus
}

// View returns a read-only snapshot of a household at day now.
func (s *Service) View(now int, householdID string) (HouseholdView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || now < 0 {
		return HouseholdView{}, newError(ErrInvalidParam, "bad arguments: household=%q now=%d", householdID, now)
	}
	if err := s.checkClock(now); err != nil {
		return HouseholdView{}, err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return HouseholdView{}, err
	}
	v := HouseholdView{Prepay: h.Prepay, TotalPaid: h.TotalPaid, Bills: map[string]BillStatus{}}
	for id, b := range h.bills {
		v.Bills[id] = s.billStatus(b, now)
	}
	return v, nil
}

// TotalDue returns the household's total amount due at day now: unpaid
// principal plus late fees accrued up to that day. It scans only the
// household's unclosed bills, never its closed history, and does not mutate
// any state.
func (s *Service) TotalDue(now int, householdID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || now < 0 {
		return 0, newError(ErrInvalidParam, "bad arguments: household=%q now=%d", householdID, now)
	}
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	h, err := s.getHousehold(householdID)
	if err != nil {
		return 0, err
	}
	var total int64
	s.scanCount = 0
	for _, b := range h.open {
		s.scanCount++
		total += b.unpaidPrincipal()
		total += b.accruedUnitsAt(now, s.cfg)/s.cfg.den() - b.PaidLate - b.WaivedLate
	}
	return total, nil
}
