// Package lotstock tracks vaccine lots, quarantine state and FEFO picking.
package lotstock

import "errors"

// Package-level errors.
var (
	ErrInvalidLot = errors.New("lotstock: invalid lot")
	ErrLotExists  = errors.New("lotstock: lot already exists")
	ErrNoLot      = errors.New("lotstock: lot not found")
	ErrNoStock    = errors.New("lotstock: no usable stock")
)

// Lot is one inventory batch.
type Lot struct {
	Lot    string
	Series string
	Exp    int
	Qty    int
	Quar   bool
}

// Stock holds all lots. Concurrency is serialized by the caller.
type Stock struct {
	lots map[string]*Lot
}

// New creates an empty stock.
func New() *Stock {
	return &Stock{lots: map[string]*Lot{}}
}

// Add registers a lot. Lot/series must be non-empty, exp in [0,1e6],
// qty in [0,1e6]. A lot with zero quantity may still be registered.
func (s *Stock) Add(l Lot) error {
	if l.Lot == "" || l.Series == "" || l.Exp < 0 || l.Exp > 1_000_000 || l.Qty < 0 || l.Qty > 1_000_000 {
		return ErrInvalidLot
	}
	if _, ok := s.lots[l.Lot]; ok {
		return ErrLotExists
	}
	cp := l
	s.lots[l.Lot] = &cp
	return nil
}

// Quarantine isolates (on=true) or releases (on=false) a lot.
func (s *Stock) Quarantine(lot string, on bool) error {
	l, ok := s.lots[lot]
	if !ok {
		return ErrNoLot
	}
	l.Quar = on
	return nil
}

// Get returns a copy of a lot.
func (s *Stock) Get(lot string) (Lot, bool) {
	l, ok := s.lots[lot]
	if !ok {
		return Lot{}, false
	}
	return *l, true
}

// Pick deducts one dose from the usable lot of series at date now via
// FEFO: not quarantined, qty>0, now<exp (equal to exp means expired);
// earliest exp wins, ties broken by lot id byte order.
func (s *Stock) Pick(series string, now int) (string, error) {
	best := ""
	bestExp := 0
	for id, l := range s.lots {
		if l.Series != series || l.Quar || l.Qty <= 0 || now >= l.Exp {
			continue
		}
		if best == "" || l.Exp < bestExp || (l.Exp == bestExp && id < best) {
			best = id
			bestExp = l.Exp
		}
	}
	if best == "" {
		return "", ErrNoStock
	}
	s.lots[best].Qty--
	return best, nil
}

// Qty reports the remaining quantity of a lot.
func (s *Stock) Qty(lot string) int {
	if l, ok := s.lots[lot]; ok {
		return l.Qty
	}
	return 0
}
