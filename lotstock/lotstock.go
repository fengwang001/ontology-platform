package lotstock

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalid = errors.New("lotstock: invalid argument")
	ErrMissing = errors.New("lotstock: lot not found")
	ErrExists  = errors.New("lotstock: lot already exists")
)

type Lot struct {
	Lot    string
	Series string
	Exp    int
	Qty    int
	Quar   bool
}

type Store struct {
	mu   sync.Mutex
	lots map[string]*Lot
}

func NewStore() *Store { return &Store{lots: map[string]*Lot{}} }

func (s *Store) AddLot(lot, series string, exp, qty int) error {
	if lot == "" || series == "" || exp < 0 || qty < 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.lots[lot]; ok {
		return ErrExists
	}
	s.lots[lot] = &Lot{Lot: lot, Series: series, Exp: exp, Qty: qty}
	return nil
}

func (s *Store) Quarantine(lot string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lots[lot]
	if !ok {
		return ErrMissing
	}
	l.Quar = on
	return nil
}

// PickAndConsume 选出可用批次并扣减 1，返回批号；无可用批次返回 false。
// 可用：未隔离、qty>0、now<exp（恰等 exp 即过期）；exp 最小优先，并列批号字节序最小。
func (s *Store) PickAndConsume(series string, now int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Lot
	for _, l := range s.lots {
		if l.Series != series || l.Quar || l.Qty <= 0 || now >= l.Exp {
			continue
		}
		if best == nil || l.Exp < best.Exp || (l.Exp == best.Exp && l.Lot < best.Lot) {
			best = l
		}
	}
	if best == nil {
		return "", false
	}
	best.Qty--
	return best.Lot, true
}

func (s *Store) Qty(lot string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lots[lot]
	if !ok {
		return 0, false
	}
	return l.Qty, true
}

// LotInfo 返回批次快照（不存在返回 nil）。
func (s *Store) LotInfo(lot string) *Lot {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lots[lot]
	if !ok {
		return nil
	}
	cp := *l
	return &cp
}

// Snapshot 返回全部批次快照，按批号排序。
func (s *Store) Snapshot() []Lot {
	s.mu.Lock()
	out := make([]Lot, 0, len(s.lots))
	for _, l := range s.lots {
		out = append(out, *l)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Lot < out[j].Lot })
	return out
}
