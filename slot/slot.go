package slot

import (
	"sort"

	"ontology"
)

// Kind 标识库位类型：Bulk 为整托位，Pick 为拣选位。
type Kind int

const (
	Bulk Kind = iota + 1
	Pick
)

// Location 是单个库位的库存账。
type Location struct {
	ID       ontology.ID
	Kind     Kind
	SKU      ontology.ID
	OnHand   int64
	Reserved int64
	Locked   bool
}

// Available 返回此刻可用量；锁定库位可用量恒为 0。
func (l *Location) Available() int64 {
	if l.Locked {
		return 0
	}
	return l.OnHand - l.Reserved
}

// Store 保存全部 SKU 整托量、库位账与每 SKU 的有序库位索引。
// 本类型自身不加锁；由 wave.Manager 的全局锁提供并发安全。
type Store struct {
	pallets map[ontology.ID]int64
	locs    map[ontology.ID]*Location
	bulk    map[ontology.ID][]ontology.ID // SKU -> Bulk 库位，编号字节序
	pick    map[ontology.ID][]ontology.ID // SKU -> Pick 库位，编号字节序
}

func NewStore() *Store {
	return &Store{
		pallets: map[ontology.ID]int64{},
		locs:    map[ontology.ID]*Location{},
		bulk:    map[ontology.ID][]ontology.ID{},
		pick:    map[ontology.ID][]ontology.ID{},
	}
}

// Pallet 返回 SKU 的整托量。
func (s *Store) Pallet(sku ontology.ID) (int64, bool) {
	p, ok := s.pallets[sku]
	return p, ok
}

// Get 返回库位账。
func (s *Store) Get(loc ontology.ID) (*Location, bool) {
	l, ok := s.locs[loc]
	return l, ok
}

// BulkIDs / PickIDs 返回该 SKU 按编号字节序排列的库位编号。
func (s *Store) BulkIDs(sku ontology.ID) []ontology.ID { return s.bulk[sku] }
func (s *Store) PickIDs(sku ontology.ID) []ontology.ID { return s.pick[sku] }

// GetRaw 返回库位的原始账目，供诊断/测试快照使用。
func (s *Store) GetRaw(loc string) (ontology.ID, int64, int64, bool) {
	l, ok := s.locs[ontology.ID(loc)]
	if !ok {
		return "", 0, 0, false
	}
	return l.ID, l.OnHand, l.Reserved, l.Locked
}

// RangeLocs 遍历全部库位编号（无序），回调 locked 标志。
func (s *Store) RangeLocs(fn func(id string, onHand, reserved int64, locked bool)) {
	for id, l := range s.locs {
		fn(string(id), l.OnHand, l.Reserved, l.Locked)
	}
}

// SetPallet 设定整托量；每个 SKU 仅能设定一次，重复为冲突。
func (s *Store) SetPallet(sku ontology.ID, p int64) error {
	if !ontology.ValidID(sku) || p < 1 || p > 1_000_000 {
		return ontology.ErrArgument
	}
	if _, ok := s.pallets[sku]; ok {
		return ontology.ErrConflict
	}
	s.pallets[sku] = p
	return nil
}

// PutStock 向库位加库存；新库位要求其 SKU 已设整托量。
func (s *Store) PutStock(loc ontology.ID, kind Kind, sku ontology.ID, qty int64) error {
	if !ontology.ValidID(loc) || !ontology.ValidID(sku) ||
		(kind != Bulk && kind != Pick) || qty < 1 || qty > 1_000_000_000 {
		return ontology.ErrArgument
	}
	if l, ok := s.locs[loc]; ok {
		if l.Locked {
			return ontology.ErrState
		}
		if l.Kind != kind || l.SKU != sku {
			return ontology.ErrConflict
		}
		l.OnHand += qty
		return nil
	}
	// 新库位：SKU 未设整托量视为不存在。
	if _, ok := s.pallets[sku]; !ok {
		return ontology.ErrNotFound
	}
	s.locs[loc] = &Location{ID: loc, Kind: kind, SKU: sku, OnHand: qty}
	s.insertIndex(sku, kind, loc)
	return nil
}

// Unlock 把已锁定库位的在库置为 counted 并解锁。
func (s *Store) Unlock(loc ontology.ID, counted int64) error {
	if !ontology.ValidID(loc) || counted < 0 || counted > 1_000_000_000 {
		return ontology.ErrArgument
	}
	l, ok := s.locs[loc]
	if !ok {
		return ontology.ErrNotFound
	}
	if !l.Locked {
		return ontology.ErrState
	}
	l.Locked = false
	l.OnHand = counted
	// 锁定时其上预占已全部随 ShortPick 删除，Reserved 恒为 0。
	l.Reserved = 0
	return nil
}

// Reserve 在库位上记一笔预占，qty 必须不超过此刻可用量。
func (s *Store) Reserve(loc ontology.ID, qty int64) {
	s.locs[loc].Reserved += qty
}

// Release 撤销库位上的预占。
func (s *Store) Release(loc ontology.ID, qty int64) {
	s.locs[loc].Reserved -= qty
}

// Pick 从库位拣出：onHand 与 reserved 各减 qty。
func (s *Store) Pick(loc ontology.ID, qty int64) {
	l := s.locs[loc]
	l.OnHand -= qty
	l.Reserved -= qty
}

// ShortPick 实拣 found：onHand 减 found、reserved 减 reservedQty，随后锁定。
func (s *Store) ShortPick(loc ontology.ID, found, reservedQty int64) {
	l := s.locs[loc]
	l.OnHand -= found
	l.Reserved -= reservedQty
	l.Locked = true
}

func (s *Store) insertIndex(sku ontology.ID, kind Kind, loc ontology.ID) {
	idx := s.bulk
	if kind == Pick {
		idx = s.pick
	}
	ids := idx[sku]
	pos := sort.SearchStrings(asStrings(ids), string(loc))
	ids = append(ids, "")
	copy(ids[pos+1:], ids[pos:])
	ids[pos] = loc
	idx[sku] = ids
}

func asStrings(ids []ontology.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}
