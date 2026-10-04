// Package slot 维护波次拣货的库位库存台账。
package slot

import "errors"

var (
	ErrInvalidArg  = errors.New("invalid argument")
	ErrNotFound    = errors.New("not found")
	ErrBadState    = errors.New("state mismatch")
	ErrQtyMismatch = errors.New("quantity mismatch")
	ErrConflict    = errors.New("conflict")
)

// Kind 为库位类型。
type Kind int

const (
	Bulk Kind = iota + 1 // 整托位
	Pick                 // 拣选位
)

// Location 为只读库位视图。
type Location struct {
	ID       string
	Kind     Kind
	SKU      string
	OnHand   int64
	Reserved int64
	Locked   bool
}

// Store 是库位台账；自身不加锁，由上层 wave.Coordinator 串行化。
type Store struct {
	pallets map[string]int64
	locs    map[string]*Location
	bySKU   map[string]skuIndex
}

type skuIndex struct {
	bulk []string // 库位编号字节序
	pick []string
}

// NewStore 创建空台账。
func NewStore() *Store {
	return &Store{
		pallets: map[string]int64{},
		locs:    map[string]*Location{},
		bySKU:   map[string]skuIndex{},
	}
}

func validName(s string) bool {
	return len(s) >= 1 && len(s) <= 32
}

func insertSorted(list []string, v string) []string {
	i := 0
	for i < len(list) && list[i] < v {
		i++
	}
	list = append(list, "")
	copy(list[i+1:], list[i:])
	list[i] = v
	return list
}

// SetPallet 设定整托量；同一 SKU 重复设定即冲突。
func (s *Store) SetPallet(sku string, p int64) error {
	if !validName(sku) || p < 1 || p > 1_000_000 {
		return ErrInvalidArg
	}
	if old, ok := s.pallets[sku]; ok {
		if old == p {
			return ErrConflict
		}
		return ErrConflict
	}
	s.pallets[sku] = p
	return nil
}

// PutStock 在库位上新设或追加在库量。
func (s *Store) PutStock(loc string, kind Kind, sku string, qty int64) error {
	if !validName(loc) || !validName(sku) || (kind != Bulk && kind != Pick) ||
		qty < 1 || qty > 1_000_000_000 {
		return ErrInvalidArg
	}
	if _, ok := s.pallets[sku]; !ok {
		return ErrNotFound
	}
	if cur, ok := s.locs[loc]; ok {
		if cur.Locked {
			return ErrBadState
		}
		if cur.Kind != kind || cur.SKU != sku {
			return ErrConflict
		}
		cur.OnHand += qty
		return nil
	}
	s.locs[loc] = &Location{ID: loc, Kind: kind, SKU: sku, OnHand: qty}
	idx := s.bySKU[sku]
	if kind == Bulk {
		idx.bulk = insertSorted(idx.bulk, loc)
	} else {
		idx.pick = insertSorted(idx.pick, loc)
	}
	s.bySKU[sku] = idx
	return nil
}

// Reserve 增加库位的已预占账（调用方保证不超可用）。
func (s *Store) Reserve(loc string, qty int64) error {
	if qty < 1 {
		return ErrInvalidArg
	}
	cur, ok := s.locs[loc]
	if !ok {
		return ErrNotFound
	}
	cur.Reserved += qty
	return nil
}

// Release 回退预占账（整单撤销时用）。
func (s *Store) Release(loc string, qty int64) {
	if cur, ok := s.locs[loc]; ok {
		cur.Reserved -= qty
	}
}

// Pick 拣货：onHand 与 reserved 同减。
func (s *Store) Pick(loc string, qty int64) error {
	if qty < 1 {
		return ErrInvalidArg
	}
	cur, ok := s.locs[loc]
	if !ok {
		return ErrNotFound
	}
	cur.OnHand -= qty
	cur.Reserved -= qty
	return nil
}

// Adjust 短拣实拣：仅 onHand 减 found（记录已删，reserved 由上层先减）。
func (s *Store) Adjust(loc string, found int64) error {
	cur, ok := s.locs[loc]
	if !ok {
		return ErrNotFound
	}
	cur.OnHand -= found
	return nil
}

// Lock 锁定库位。
func (s *Store) Lock(loc string) error {
	cur, ok := s.locs[loc]
	if !ok {
		return ErrNotFound
	}
	cur.Locked = true
	return nil
}

// Unlock 盘点后重置在库并解锁（reserved 由上层保证为 0）。
func (s *Store) Unlock(loc string, counted int64) error {
	if counted < 0 || counted > 1_000_000_000 {
		return ErrInvalidArg
	}
	cur, ok := s.locs[loc]
	if !ok {
		return ErrNotFound
	}
	if !cur.Locked {
		return ErrBadState
	}
	cur.Locked = false
	cur.OnHand = counted
	cur.Reserved = 0
	return nil
}

// Get 返回库位只读快照。
func (s *Store) Get(loc string) (Location, bool) {
	cur, ok := s.locs[loc]
	if !ok {
		return Location{}, false
	}
	return *cur, true
}

// Pallet 返回 SKU 的整托量。
func (s *Store) Pallet(sku string) (int64, bool) {
	p, ok := s.pallets[sku]
	return p, ok
}

// SKULocs 为一次分配枚举的库位快照。
type SKULocs struct {
	Bulk []LocAvail
	Pick []LocAvail
}

// LocAvail 为库位此刻的可用量。
type LocAvail struct {
	Loc   string
	Avail int64
}

// Locs 枚举某 SKU 全部库位此刻的可用量，Bulk/Pick 分别按编号字节序；
// 已锁定库位可用量为 0，仍在切片中返回，由调用方决定是否考察。
func (s *Store) Locs(sku string) SKULocs {
	idx := s.bySKU[sku]
	out := SKULocs{
		Bulk: make([]LocAvail, 0, len(idx.bulk)),
		Pick: make([]LocAvail, 0, len(idx.pick)),
	}
	avail := func(id string) int64 {
		cur := s.locs[id]
		if cur.Locked {
			return 0
		}
		return cur.OnHand - cur.Reserved
	}
	for _, id := range idx.bulk {
		out.Bulk = append(out.Bulk, LocAvail{Loc: id, Avail: avail(id)})
	}
	for _, id := range idx.pick {
		out.Pick = append(out.Pick, LocAvail{Loc: id, Avail: avail(id)})
	}
	return out
}

// ReservedTotal 为保留给测试/不变量核对的在库已预占总量。
func (s *Store) ReservedTotal() int64 {
	var n int64
	for _, cur := range s.locs {
		n += cur.Reserved
	}
	return n
}
