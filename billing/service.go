package billing

import (
	"fmt"
	"sort"
	"sync"
)

// Household holds the per-household ledger. open contains only unclosed
// bills, ordered by (dueDay, seq); closed bills are archived and never
// iterated by payment or query paths.
type Household struct {
	id     string
	bills  map[string]*Bill
	open   []*Bill
	closed []*Bill

	prepay         int64
	totalPaid      int64 // sum of accepted payment amounts
	allocPrincipal int64 // total principal offset by payments
	allocLateFee   int64 // total late fee offset by payments
}

func (h *Household) insertOpen(b *Bill) {
	i := sort.Search(len(h.open), func(i int) bool {
		o := h.open[i]
		return o.dueDay > b.dueDay || (o.dueDay == b.dueDay && o.seq > b.seq)
	})
	h.open = append(h.open, nil)
	copy(h.open[i+1:], h.open[i:])
	h.open[i] = b
}

func (h *Household) removeOpen(b *Bill) {
	for i, o := range h.open {
		if o == b {
			h.open = append(h.open[:i], h.open[i+1:]...)
			return
		}
	}
}

// closeBill marks a bill closed and moves it out of the open set; its
// dunning stage is frozen from now on.
func (h *Household) closeBill(b *Bill) {
	b.closed = true
	h.removeOpen(b)
	h.closed = append(h.closed, b)
}

// maybeClose closes the bill when nothing is owed on it anymore.
func (h *Household) maybeClose(b *Bill) {
	if !b.closed && b.unpaidPrincipal() == 0 && b.unpaidLateFee() == 0 {
		h.closeBill(b)
	}
}

// Service is the billing engine. A single mutex serializes all
// operations, so any concurrent interleaving is equivalent to the
// serial order in which the mutex was acquired.
type Service struct {
	mu         sync.Mutex
	cfg        Config
	lastNow    int64
	seq        int64
	households map[string]*Household

	queryVisits int // bills visited by the last TotalDue call (cost proof hook)
}

// NewService validates the configuration and returns an empty service.
func NewService(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, lastNow: -1, households: map[string]*Household{}}, nil
}

// checkClock enforces the monotonic clock. It must be called after
// parameter validation and before any state lookup.
func (s *Service) checkClock(now int64) *Error {
	if now < 0 {
		return paramErr("now must be >= 0, got %d", now)
	}
	if now < s.lastNow {
		return clockErr("now %d is before last accepted now %d", now, s.lastNow)
	}
	return nil
}

func (s *Service) household(id string) (*Household, *Error) {
	h, ok := s.households[id]
	if !ok {
		return nil, notFoundErr("household %q not found", id)
	}
	return h, nil
}

func (h *Household) bill(id string) (*Bill, *Error) {
	b, ok := h.bills[id]
	if !ok {
		return nil, notFoundErr("bill %q not found", id)
	}
	return b, nil
}

// AddHousehold registers a household. Re-adding an existing id is an
// invalid-state error.
func (s *Service) AddHousehold(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return paramErr("household id is empty")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.households[id]; ok {
		return stateErr("household %q already exists", id)
	}
	s.lastNow = now
	s.households[id] = &Household{id: id, bills: map[string]*Bill{}}
	return nil
}

// GenerateBill creates a bill for a period with the given principal and
// due day, and returns its id. Any prepayment balance is immediately
// applied to the new bill (principal only; a fresh bill has no late fee).
func (s *Service) GenerateBill(householdID, period string, principal, dueDay, now int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if householdID == "" || period == "" {
		return "", paramErr("household id and period must be non-empty")
	}
	if principal <= 0 {
		return "", paramErr("principal must be positive, got %d", principal)
	}
	if dueDay < 0 {
		return "", paramErr("due day must be >= 0, got %d", dueDay)
	}
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	h, herr := s.household(householdID)
	if herr != nil {
		return "", herr
	}
	s.lastNow = now
	s.seq++
	b := &Bill{
		seq:            s.seq,
		id:             fmt.Sprintf("B%06d", s.seq),
		period:         period,
		dueDay:         dueDay,
		principal:      principal,
		accruedThrough: dueDay + s.cfg.GraceDays,
	}
	if h.prepay > 0 {
		x := min(h.prepay, principal)
		b.principalPaid = x
		h.prepay -= x
		h.allocPrincipal += x
	}
	h.bills[b.id] = b
	if b.unpaidPrincipal() == 0 {
		b.closed = true
		h.closed = append(h.closed, b)
	} else {
		b.advanceStage(s.cfg, now)
		h.insertOpen(b)
	}
	return b.id, nil
}
