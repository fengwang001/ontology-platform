// Package lotstock 实现疫苗批次库存与效期管理。
package lotstock

import (
	"errors"
	"sync"
)

// Lot 为一个疫苗批次。
type Lot struct {
	Name        string
	Series      string
	Exp         int
	Qty         int
	Quarantined bool
}

// Stock 为批次库存，可并发使用。
type Stock struct {
	mu   sync.Mutex
	lots map[string]*Lot
}

// New 创建空库存。
func New() *Stock {
	return &Stock{lots: make(map[string]*Lot)}
}

// Has 报告批次是否存在。
func (s *Stock) Has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.lots[name]
	return ok
}

// AddLot 新增批次，重名报错。
func (s *Stock) AddLot(name, series string, exp, qty int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.lots[name]; ok {
		return errors.New("lotstock: 批次已存在")
	}
	s.lots[name] = &Lot{Name: name, Series: series, Exp: exp, Qty: qty}
	return nil
}

// Quarantine 隔离或解除批次，批次不存在报错。
func (s *Stock) Quarantine(name string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lots[name]
	if !ok {
		return errors.New("lotstock: 批次不存在")
	}
	l.Quarantined = on
	return nil
}

// Acquire 在该系列「未隔离、qty>0、now<exp（恰等 exp 即过期）」的批次中
// 取 exp 最小者（并列取批号字节序小者），扣 1 并返回批号；无可用批次返回 false。
func (s *Stock) Acquire(series string, now int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Lot
	for _, l := range s.lots {
		if l.Series != series || l.Quarantined || l.Qty <= 0 || now >= l.Exp {
			continue
		}
		if best == nil || l.Exp < best.Exp || (l.Exp == best.Exp && l.Name < best.Name) {
			best = l
		}
	}
	if best == nil {
		return "", false
	}
	best.Qty--
	return best.Name, true
}

// Qty 返回批次当前余量（供测试核对）。
func (s *Stock) Qty(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.lots[name]; ok {
		return l.Qty
	}
	return -1
}
