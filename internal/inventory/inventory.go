package inventory

import (
	"sort"
	"sync"
)

// Lock 锁定库存单元。
func (c *Cell) Lock() { c.mu.Lock() }

// Unlock 解锁库存单元。
func (c *Cell) Unlock() { c.mu.Unlock() }

// Get 返回 (可用量, 冻结量)，调用方须持有锁（或接受弱一致读）。
func (c *Cell) Get() (int64, int64) { return c.available, c.frozen }

// Apply 在持锁状态下调整可用量与冻结量。
func (c *Cell) Apply(deltaAvailable, deltaFrozen int64) {
	c.available += deltaAvailable
	c.frozen += deltaFrozen
}

// Cell 记录某仓库某商品的可用量与冻结量。
type Cell struct {
	mu        sync.Mutex
	available int64
	frozen    int64
}

func (c *Cell) snapshot() (int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.available, c.frozen
}

// Store 管理全部仓库 × 商品的库存单元。
type Store struct {
	mu    sync.RWMutex
	cells map[string]map[string]*Cell
}

func NewStore(initial map[string]map[string]int64) *Store {
	s := &Store{cells: make(map[string]map[string]*Cell)}
	for wh, items := range initial {
		for item, qty := range items {
			if qty <= 0 {
				continue
			}
			s.cellLocked(wh, item).available = qty
		}
	}
	return s
}

// cellLocked 获取或创建库存单元；调用方持有 s.mu。
func (s *Store) cellLocked(warehouse, item string) *Cell {
	items, ok := s.cells[warehouse]
	if !ok {
		items = make(map[string]*Cell)
		s.cells[warehouse] = items
	}
	c, ok := items[item]
	if !ok {
		c = &Cell{}
		items[item] = c
	}
	return c
}

// Cell 获取或创建库存单元。
func (s *Store) Cell(warehouse, item string) *Cell {
	s.mu.RLock()
	c, ok := s.cells[warehouse][item]
	s.mu.RUnlock()
	if ok {
		return c
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cellLocked(warehouse, item)
}

func (s *Store) Available(warehouse, item string) int64 {
	a, _ := s.Snapshot(warehouse, item)
	return a
}

func (s *Store) Frozen(warehouse, item string) int64 {
	_, f := s.Snapshot(warehouse, item)
	return f
}

// Snapshot 返回某仓某商品的 (可用量, 冻结量) 一致快照。
func (s *Store) Snapshot(warehouse, item string) (int64, int64) {
	return s.Cell(warehouse, item).snapshot()
}

// ItemCells 返回持有某商品的全部库存单元（含仓库名），按键序排列。
func (s *Store) ItemCells(item string) []CellRef {
	s.mu.RLock()
	defer s.mu.RUnlock()
	refs := make([]CellRef, 0, len(s.cells))
	for wh, items := range s.cells {
		if c, ok := items[item]; ok {
			refs = append(refs, CellRef{Warehouse: wh, Cell: c})
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Warehouse < refs[j].Warehouse })
	return refs
}

type CellRef struct {
	Warehouse string
	Cell      *Cell
}

type Req struct {
	Warehouse string
	Item      string
	Qty       int64
	Cell      *Cell
}

// LockCell 获取某仓某商品库存单元的互斥锁（供上层实现提交协议）。
func (s *Store) LockCell(warehouse, item string) *Cell {
	c := s.Cell(warehouse, item)
	c.mu.Lock()
	return c
}

// LockCells 按全局键序锁定请求列表，返回填入了 Cell 的列表。
func (s *Store) LockCells(reqs []Req) []Req {
	for i := range reqs {
		if reqs[i].Cell == nil {
			reqs[i].Cell = s.Cell(reqs[i].Warehouse, reqs[i].Item)
		}
	}
	lockAll(reqs)
	return reqs
}

// UnlockReqs 释放 LockCells 获取的全部锁。
func (s *Store) UnlockReqs(reqs []Req) {
	for _, r := range reqs {
		r.Cell.mu.Unlock()
	}
}

// lockAll 按全局键序获取一批库存单元的锁，返回加锁顺序对应的单元。
// 不同订单之间因此不会出现锁顺序环（无跨订单死锁）。
func lockAll(reqs []Req) []*Cell {
	cells := make([]*Cell, len(reqs))
	order := make([]int, len(reqs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := reqs[order[a]], reqs[order[b]]
		if x.Warehouse != y.Warehouse {
			return x.Warehouse < y.Warehouse
		}
		return x.Item < y.Item
	})
	seen := make([]*Cell, 0, len(reqs))
	for _, idx := range order {
		c := reqs[idx].Cell
		c.mu.Lock()
		seen = append(seen, c)
		cells[idx] = c
	}
	return cells
}

func unlockAll(cells []*Cell) {
	for _, c := range cells {
		c.mu.Unlock()
	}
}

// FreezeAll 整批冻结：所有行可用量都充足才生效（全有或全无）。
// 任一不足返回 (最小不足行下标, false)，且不产生任何变更并释放全部锁；
// 成功时保持锁，由调用方在提交或回滚后通过 UnlockReqs 释放。
func (s *Store) FreezeAll(reqs []Req) (int, bool) {
	for i := range reqs {
		reqs[i].Cell = s.Cell(reqs[i].Warehouse, reqs[i].Item)
	}
	locked := lockAll(reqs)

	failIdx := -1
	for i, r := range reqs {
		if r.Cell.available < r.Qty && (failIdx == -1 || i < failIdx) {
			failIdx = i
		}
	}
	if failIdx != -1 {
		unlockAll(locked)
		return failIdx, false
	}
	for _, r := range reqs {
		r.Cell.available -= r.Qty
		r.Cell.frozen += r.Qty
	}
	return -1, true
}

// UnfreezeAll 释放一批冻结量（取消）。调用方保证冻结量充足。
func (s *Store) UnfreezeAll(reqs []Req) {
	for i := range reqs {
		reqs[i].Cell = s.Cell(reqs[i].Warehouse, reqs[i].Item)
	}
	locked := lockAll(reqs)
	defer unlockAll(locked)
	for _, r := range reqs {
		r.Cell.frozen -= r.Qty
		r.Cell.available += r.Qty
	}
}

// IssueFrozenAll 将一批冻结量从源仓扣除（发出：冻结 → 在途）。
func (s *Store) IssueFrozenAll(reqs []Req) {
	for i := range reqs {
		reqs[i].Cell = s.Cell(reqs[i].Warehouse, reqs[i].Item)
	}
	locked := lockAll(reqs)
	defer unlockAll(locked)
	for _, r := range reqs {
		r.Cell.frozen -= r.Qty
	}
}

// AddAvailable 增加某仓某商品的可用量（收货 / 找回）。
func (s *Store) AddAvailable(warehouse, item string, qty int64) {
	c := s.Cell(warehouse, item)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.available += qty
}
