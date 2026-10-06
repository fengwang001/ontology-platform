package slotting

import "sync"

// locEntry 为单个货位的运行期数据（配置 + 在位托盘）。
type locEntry struct {
	config  LocationConfig
	weight  int
	pallets []*Pallet
}

// Store 是占用账与候选索引的唯一权威，自带读写锁：
// 读操作获取某一已提交状态的一致快照；写操作在临界区内原子提交，
// 因此外部不可能观察到移库进行到一半的中间状态。
type Store struct {
	mu sync.RWMutex

	locs    map[Coord]*locEntry
	pallets map[string]*Pallet
	where   map[string]Coord // 托盘编号 -> 所在货位

	// byProduct[product]：状态正常、仍有剩余容量、且已存放该商品的货位。
	byProduct map[string]*orderedSet[Coord]
	// productAll[product]：存放该商品的全部货位（含冻结），供查询使用。
	productAll map[string]*orderedSet[Coord]
	// empties：状态正常、完全为空的货位。
	empties *orderedSet[Coord]
	// nonFull：状态正常、仍有剩余容量的全部货位。
	nonFull *orderedSet[Coord]
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		locs:       map[Coord]*locEntry{},
		pallets:    map[string]*Pallet{},
		where:      map[string]Coord{},
		byProduct:  map[string]*orderedSet[Coord]{},
		productAll: map[string]*orderedSet[Coord]{},
		empties:    newOrderedSet[Coord](coordLess),
		nonFull:    newOrderedSet[Coord](coordLess),
	}
}

func (s *Store) lock()    { s.mu.Lock() }
func (s *Store) unlock()  { s.mu.Unlock() }
func (s *Store) rlock()   { s.mu.RLock() }
func (s *Store) runlock() { s.mu.RUnlock() }

// AddLocation 注册货位；坐标重复为参数非法。
func (s *Store) AddLocation(cfg LocationConfig) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if _, ok := s.locs[cfg.Coord]; ok {
		return errf(ReasonInvalidParam, "duplicate location %v", cfg.Coord)
	}
	allowed := make(map[Category]bool, len(cfg.Allowed))
	for cat := range cfg.Allowed {
		allowed[cat] = true
	}
	cfg.Allowed = allowed
	s.locs[cfg.Coord] = &locEntry{config: cfg}
	if cfg.Status == StatusNormal {
		s.empties.insert(cfg.Coord)
		s.nonFull.insert(cfg.Coord)
	}
	return nil
}

// loc 按坐标取货位（持锁调用）。
func (s *Store) loc(c Coord) (*locEntry, bool) {
	e, ok := s.locs[c]
	return e, ok
}

// neighbors 返回实际存在的相邻货位，冻结货位同样返回（隔离约束持续有效）。
func (s *Store) neighbors(c Coord) []*locEntry {
	var out []*locEntry
	for _, nk := range neighborKeys(c) {
		if e, ok := s.locs[nk]; ok {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) productSet(product string) *orderedSet[Coord] {
	set := s.byProduct[product]
	if set == nil {
		set = newOrderedSet[Coord](coordLess)
		s.byProduct[product] = set
	}
	return set
}

func (s *Store) productAllSet(product string) *orderedSet[Coord] {
	set := s.productAll[product]
	if set == nil {
		set = newOrderedSet[Coord](coordLess)
		s.productAll[product] = set
	}
	return set
}

// place 把托盘放入货位（调用方已完成全部约束校验）并同步索引。
func (s *Store) place(loc *locEntry, p Pallet) {
	c := loc.config.Coord
	stored := p
	loc.pallets = append(loc.pallets, &stored)
	loc.weight += p.Weight
	s.pallets[p.ID] = loc.pallets[len(loc.pallets)-1]
	s.where[p.ID] = c

	if loc.config.Status != StatusNormal {
		s.productAllSet(p.Product).insert(c)
		return
	}
	if len(loc.pallets) == 1 {
		s.empties.erase(c)
		s.productAllSet(p.Product).insert(c)
	}
	s.productSet(p.Product).insert(c)
	if len(loc.pallets) >= loc.config.Capacity {
		s.nonFull.erase(c)
		s.productSet(p.Product).erase(c)
	}
}

// remove 从货位取出指定托盘并同步索引。
func (s *Store) remove(loc *locEntry, palletID string) Pallet {
	idx := -1
	var removed Pallet
	for i, q := range loc.pallets {
		if q.ID == palletID {
			idx = i
			removed = *q
			break
		}
	}
	loc.pallets = append(loc.pallets[:idx], loc.pallets[idx+1:]...)
	loc.weight -= removed.Weight
	delete(s.pallets, palletID)
	delete(s.where, palletID)

	c := loc.config.Coord
	if loc.config.Status != StatusNormal {
		if len(loc.pallets) == 0 {
			s.productAllSet(removed.Product).erase(c)
		}
		return removed
	}
	wasFull := len(loc.pallets)+1 >= loc.config.Capacity
	if len(loc.pallets) == 0 {
		s.empties.insert(c)
		s.productSet(removed.Product).erase(c)
		s.productAllSet(removed.Product).erase(c)
	}
	if wasFull {
		s.nonFull.insert(c)
		if len(loc.pallets) > 0 {
			s.productSet(removed.Product).insert(c)
		}
	}
	return removed
}

// setFrozen 切换冻结状态并维护索引；在位托盘保留。
func (s *Store) setFrozen(c Coord, frozen bool) {
	loc := s.locs[c]
	if frozen {
		loc.config.Status = StatusFrozen
		s.empties.erase(c)
		s.nonFull.erase(c)
		if len(loc.pallets) > 0 {
			s.productSet(loc.pallets[0].Product).erase(c)
		}
		return
	}
	loc.config.Status = StatusNormal
	if len(loc.pallets) < loc.config.Capacity {
		s.nonFull.insert(c)
		if len(loc.pallets) == 0 {
			s.empties.insert(c)
		}
		if len(loc.pallets) > 0 {
			s.productSet(loc.pallets[0].Product).insert(c)
		}
	}
}
