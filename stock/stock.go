// Package stock 登记物料的库存参数与批量规则。
package stock

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalid  = errors.New("stock: invalid parameter")
	ErrConflict = errors.New("stock: item already registered")
)

const (
	maxQty  = int64(1_000_000_000)
	maxLead = 52
	maxLot  = int64(1_000_000)
)

// Item 是一种物料的库存与批量参数。
type Item struct {
	Name    string
	OnHand  int64
	SS      int64
	Lead    int
	LotMin  int64
	LotMult int64
}

// ValidName 报告物料号是否为 1 到 32 字节的非空字节串。
func ValidName(name string) bool {
	return len(name) >= 1 && len(name) <= 32
}

// Register 是物料登记表，并发安全；version 只随被接受的变更递增。
type Register struct {
	mu      sync.RWMutex
	items   map[string]Item
	version int64
}

func New() *Register {
	return &Register{items: make(map[string]Item)}
}

func (r *Register) AddItem(name string, onHand, ss int64, lead int, lotMin, lotMult int64) error {
	if !ValidName(name) ||
		onHand < 0 || onHand > maxQty || ss < 0 || ss > maxQty ||
		lead < 0 || lead > maxLead ||
		lotMin < 1 || lotMin > maxLot || lotMult < 1 || lotMult > maxLot {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[name]; ok {
		return ErrConflict
	}
	r.items[name] = Item{Name: name, OnHand: onHand, SS: ss, Lead: lead, LotMin: lotMin, LotMult: lotMult}
	r.version++
	return nil
}

func (r *Register) Get(name string) (Item, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.items[name]
	return it, ok
}

// All 返回全部物料，按物料号字节序。
func (r *Register) All() []Item {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Item, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Version 返回被接受变更的次数。
func (r *Register) Version() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// Lot 返回净需求 net(>0) 对应的订货量 max(lotMin, ceil(net/lotMult)*lotMult)。
func Lot(it Item, net int64) int64 {
	q := (net + it.LotMult - 1) / it.LotMult * it.LotMult
	if q < it.LotMin {
		q = it.LotMin
	}
	return q
}
