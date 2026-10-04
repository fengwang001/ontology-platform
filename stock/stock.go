// Package stock 登记物料的库存参数与批量规则，并定义各包共用的哨兵错误。
package stock

import (
	"errors"
	"fmt"
	"sync"
)

// 哨兵错误，供 errors.Is 区分；bom 与 mrp 包重导出同一实例。
var (
	ErrInvalid  = errors.New("invalid argument")
	ErrNotExist = errors.New("item not registered")
	ErrConflict = errors.New("item already registered")
)

const (
	maxItemIDLen = 32
	maxQty       = 1_000_000_000 // onHand / ss 上限 1e9
	maxLead      = 52
	maxLot       = 1_000_000 // lotMin / lotMult 上限 1e6
)

// ValidItemID 报告物料号是否为 1 到 32 字节的非空字节串。
func ValidItemID(id []byte) bool { return len(id) >= 1 && len(id) <= maxItemIDLen }

// Item 为单个物料的库存与批量参数。
type Item struct {
	OnHand  uint64 // 期初库存
	SS      uint64 // 安全库存
	Lead    uint64 // 提前期（期）
	LotMin  uint64 // 最小批量
	LotMult uint64 // 批量倍数
}

// Stock 为物料登记表，可并发使用。
type Stock struct {
	mu    sync.RWMutex
	items map[string]Item
}

// New 返回空登记表。
func New() *Stock { return &Stock{items: make(map[string]Item)} }

// AddItem 登记物料；参数非法报 ErrInvalid，物料号重复报 ErrConflict。
// 被拒绝时不改任何状态。
func (s *Stock) AddItem(id []byte, onHand, ss, lead, lotMin, lotMult uint64) error {
	if !ValidItemID(id) || onHand > maxQty || ss > maxQty || lead > maxLead ||
		lotMin < 1 || lotMin > maxLot || lotMult < 1 || lotMult > maxLot {
		return fmt.Errorf("stock: AddItem(%q): %w", id, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[string(id)]; ok {
		return fmt.Errorf("stock: AddItem(%q): %w", id, ErrConflict)
	}
	s.items[string(id)] = Item{OnHand: onHand, SS: ss, Lead: lead, LotMin: lotMin, LotMult: lotMult}
	return nil
}

// Has 报告物料是否已登记。
func (s *Stock) Has(id []byte) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[string(id)]
	return ok
}

// Snapshot 返回当前全部物料参数的副本。
func (s *Stock) Snapshot() map[string]Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Item, len(s.items))
	for k, v := range s.items {
		out[k] = v
	}
	return out
}
