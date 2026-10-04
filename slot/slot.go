package slot

import "sync"

// Sentinel errors: 所有包共享同一套错误标识，上层包以别名重新导出。
var (
	ErrInvalid   = errInvalid{}
	ErrNotFound  = errNotFound{}
	ErrState     = errState{}
	ErrConflict  = errConflict{}
	ErrShortPick = errShortPick{}
	ErrOverQty   = errOverQty{}
)

type errInvalid struct{}
type errNotFound struct{}
type errState struct{}
type errConflict struct{}
type errShortPick struct{}
type errOverQty struct{}

func (errInvalid) Error() string   { return "invalid argument" }
func (errNotFound) Error() string  { return "not found" }
func (errState) Error() string     { return "invalid state" }
func (errConflict) Error() string  { return "conflict" }
func (errShortPick) Error() string { return "insufficient on-hand stock" }
func (errOverQty) Error() string   { return "quantity exceeds limit" }

// Slot 是一个拣选位的事实状态（不含任务）。字段对同模块 task 包可见，
// 由调用方在持锁状态下访问。
type Slot struct {
	Loc       string
	SKU       string
	Min       int64
	Max       int64
	Cap       int64
	C         int64
	OnHand    int64
	InTransit int64
	Starved   int64
}

// Store 持有全部库位、储备库存与单一互斥锁；所有派生量均增量维护。
type Store struct {
	mu       sync.Mutex
	slots    map[string]*Slot
	reserve  map[string]int64
	skuUsed  map[string]int64 // SKU 全部未完成任务量之和（增量维护）
	touched  int64
	touchSet map[string]struct{}
}

// NewStore 创建空库存存储。
func NewStore() *Store {
	return &Store{
		slots:    map[string]*Slot{},
		reserve:  map[string]int64{},
		skuUsed:  map[string]int64{},
		touchSet: map[string]struct{}{},
	}
}

// Lock/Unlock 暴露给 task/replen 复用同一把锁，保证等价于某一串行顺序。
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// Get 返回库位（不存在为 nil）。触碰统计：库位记录 1 条。
func (s *Store) Get(loc string) *Slot {
	sl := s.slots[loc]
	if sl != nil {
		s.touch("loc", loc)
	}
	return sl
}

// GetSKUSlot 按 SKU 返回任意一个拣选位（用于 AddReserve 的存在性判定）。
// 该全量探查仅用于 AddReserve 校验，不计入 touched（AddReserve 无触碰承诺）。
func (s *Store) GetSKUSlot(sku string) *Slot {
	for _, sl := range s.slots {
		if sl.SKU == sku {
			return sl
		}
	}
	return nil
}

// ReserveOf 返回储备库存账面量。
func (s *Store) ReserveOf(sku string) int64 { return s.reserve[sku] }

// SKUUsed 返回某 SKU 全部未完成任务量之和（增量维护值）。
func (s *Store) SKUUsed(sku string) int64 { return s.skuUsed[sku] }

// SlotSnapshot 是库位只读快照。
type SlotSnapshot struct {
	Loc       string
	SKU       string
	Min       int64
	Max       int64
	Cap       int64
	C         int64
	OnHand    int64
	InTransit int64
	Starved   int64
}

// Slots 返回全部库位按 Loc 升序的快照。
func (s *Store) Slots() []SlotSnapshot {
	locs := make([]string, 0, len(s.slots))
	for loc := range s.slots {
		locs = append(locs, loc)
	}
	sortStrings(locs)
	out := make([]SlotSnapshot, 0, len(locs))
	for _, loc := range locs {
		sl := s.slots[loc]
		out = append(out, SlotSnapshot{
			Loc: sl.Loc, SKU: sl.SKU, Min: sl.Min, Max: sl.Max, Cap: sl.Cap,
			C: sl.C, OnHand: sl.OnHand, InTransit: sl.InTransit, Starved: sl.Starved,
		})
	}
	return out
}

// ReserveSnapshot 是 SKU 储备只读快照。
type ReserveSnapshot struct {
	SKU     string
	Reserve int64
	Avail   int64
}

// Reserves 返回出现过储备或库位的 SKU 按 SKU 升序的快照。
func (s *Store) Reserves() []ReserveSnapshot {
	skus := map[string]struct{}{}
	for sku := range s.reserve {
		skus[sku] = struct{}{}
	}
	for _, sl := range s.slots {
		skus[sl.SKU] = struct{}{}
	}
	ordered := make([]string, 0, len(skus))
	for sku := range skus {
		ordered = append(ordered, sku)
	}
	sortStrings(ordered)
	out := make([]ReserveSnapshot, 0, len(ordered))
	for _, sku := range ordered {
		out = append(out, ReserveSnapshot{
			SKU:     sku,
			Reserve: s.reserve[sku],
			Avail:   s.reserve[sku] - s.skuUsed[sku],
		})
	}
	return out
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// ResetTouched 清零触碰计数（每次操作开始调用）。
func (s *Store) ResetTouched() {
	s.touched = 0
	s.touchSet = map[string]struct{}{}
}

// Touched 返回自上次 ResetTouched 以来触碰的去重记录数。
func (s *Store) Touched() int64 { return s.touched }

// touch 登记一条被触碰记录（kind 区分库位记录与任务记录），按身份去重。
func (s *Store) touch(kind, id string) {
	key := kind + ":" + id
	if _, ok := s.touchSet[key]; ok {
		return
	}
	s.touchSet[key] = struct{}{}
	s.touched++
}

// TouchTask 供 task 包登记被触碰的任务记录（按任务 ID 去重）。
func (s *Store) TouchTask(id int64) { s.touch("task", itoa(id)) }

// AddSlot 插入新拣选位（参数校验由上层 Engine 完成，冲突在此判定）。
func (s *Store) AddSlot(loc, sku string, min, max, capV, c, onHand int64) (*Slot, error) {
	if _, ok := s.slots[loc]; ok {
		return nil, ErrConflict
	}
	sl := &Slot{
		Loc: loc, SKU: sku, Min: min, Max: max, Cap: capV,
		C: c, OnHand: onHand,
	}
	s.slots[loc] = sl
	s.touch("loc", loc)
	return sl, nil
}

// AddReserve 增加某 SKU 储备库存。上限校验（累计 ≤ 1e12）由调用方完成。
func (s *Store) AddReserve(sku string, qty int64) { s.reserve[sku] += qty }

// ApplyPick 从拣选位取走 qty；库存不足返回 ErrShortPick。
func (s *Store) ApplyPick(sl *Slot, qty int64) error {
	if qty > sl.OnHand {
		return ErrShortPick
	}
	sl.OnHand -= qty
	return nil
}

// ReserveOpen 为一条新任务登记在途占用（不改储备账面；储备在 Confirm 时扣减）。
func (s *Store) ReserveOpen(sl *Slot, qty int64) {
	sl.InTransit += qty
	s.skuUsed[sl.SKU] += qty
}

// ApplyConfirm 任务到货：onHand += actual；储备与在途均按任务量全额释放。
// 短补差额即为储备盘亏。
func (s *Store) ApplyConfirm(sl *Slot, tskSku string, qty, actual int64) {
	sl.OnHand += actual
	sl.InTransit -= qty
	s.skuUsed[tskSku] -= qty
	s.reserve[tskSku] -= qty
}

// ApplyCancel 取消任务：仅释放该任务的在途占用，不动储备与 onHand。
func (s *Store) ApplyCancel(sl *Slot, qty int64) {
	sl.InTransit -= qty
	s.skuUsed[sl.SKU] -= qty
}

// BumpStarved 将库位的 Starved 计数加 1。
func (s *Store) BumpStarved(sl *Slot) { sl.Starved++ }

// Avail 返回某 SKU 当前储备可用量。
func (s *Store) Avail(sku string) int64 {
	return s.reserve[sku] - s.skuUsed[sku]
}

// itoa 避免为单个用途引入 strconv 的格式化开销。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
