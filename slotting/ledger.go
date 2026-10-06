package slotting

// store 为全部可变状态。同一时刻仅可在持有 system.mu 时访问。
type store struct {
	locations map[LocationID]*locState
	pallets   map[string]*Pallet
	where     map[string]LocationID // 已上架托盘所在货位
	products  map[string]*productIndex
	empty     *emptyIndex
	examined  int // 最近一次自动上架实际考察的货位数（测试/验证用）
}

// locState 为单个货位的运行时状态。
type locState struct {
	loc     Location
	pallets []string // 在位托盘编号，按放入顺序
	weight  int
}

// productIndex 为某商品当前已有在位托盘的货位集合，key 有序遍历由 sortedIDs 保证。
type productIndex struct {
	ids map[LocationID]struct{}
}

func newStore() *store {
	s := &store{
		locations: map[LocationID]*locState{},
		pallets:   map[string]*Pallet{},
		where:     map[string]LocationID{},
		products:  map[string]*productIndex{},
	}
	s.empty = newEmptyIndex(s.locations, s.pallets)
	return s
}

// addLocation 登记货位静态信息。
func (s *store) addLocation(loc Location) {
	st := &locState{loc: loc, pallets: []string{}}
	if st.loc.Status == "" {
		st.loc.Status = StatusNormal
	}
	s.locations[loc.ID] = st
	s.empty.add(loc.ID)
}

// productIDs 有序返回某商品当前在位的货位，保证确定性。
func (s *store) productSortedIDs(product string) []LocationID {
	pi := s.products[product]
	if pi == nil {
		return nil
	}
	ids := make([]LocationID, 0, len(pi.ids))
	for id := range pi.ids {
		ids = append(ids, id)
	}
	sortIDs(ids)
	return ids
}

// place 不做任何约束检查（检查由 checkPlace 完成），只更新台账与索引。
func (s *store) place(id LocationID, p *Pallet) {
	ls := s.locations[id]
	ls.pallets = append(ls.pallets, p.ID)
	ls.weight += p.Weight
	s.where[p.ID] = id
	pi := s.products[p.Product]
	if pi == nil {
		pi = &productIndex{ids: map[LocationID]struct{}{}}
		s.products[p.Product] = pi
	}
	pi.ids[id] = struct{}{}
	if len(ls.pallets) == 1 {
		s.empty.remove(id)
	}
	s.empty.changed(id)
}

// release 从货位取出指定托盘；返回托盘是否曾在该货位。
func (s *store) release(id LocationID, palletID string) bool {
	ls := s.locations[id]
	pos := -1
	for i, pid := range ls.pallets {
		if pid == palletID {
			pos = i
			break
		}
	}
	if pos < 0 {
		return false
	}
	p := s.pallets[palletID]
	ls.pallets = append(ls.pallets[:pos], ls.pallets[pos+1:]...)
	ls.weight -= p.Weight
	delete(s.where, palletID)
	if len(ls.pallets) == 0 {
		pi := s.products[p.Product]
		delete(pi.ids, id)
		if len(pi.ids) == 0 {
			delete(s.products, p.Product)
		}
		// 冻结货位释放后仍不参与自动上架。
		if ls.loc.Status == StatusNormal {
			s.empty.add(id)
		}
	}
	s.empty.changed(id)
	return true
}

// setStatus 改变冻结状态并维护空位索引。
func (s *store) setStatus(id LocationID, status LocationStatus) {
	ls := s.locations[id]
	if ls.loc.Status == status {
		return
	}
	ls.loc.Status = status
	switch {
	case status == StatusFrozen:
		s.empty.remove(id)
	case status == StatusNormal && len(ls.pallets) == 0:
		s.empty.add(id)
	}
	s.empty.changed(id)
}

func sortIDs(ids []LocationID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j].Less(ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// clone 深拷贝全部状态（索引随之重建），用于批量/移库的原子回滚。
func (s *store) clone() *store {
	c := &store{
		locations: make(map[LocationID]*locState, len(s.locations)),
		pallets:   make(map[string]*Pallet, len(s.pallets)),
		where:     make(map[string]LocationID, len(s.where)),
		products:  make(map[string]*productIndex, len(s.products)),
	}
	for id, ls := range s.locations {
		cp := *ls
		cp.pallets = append([]string(nil), ls.pallets...)
		allowed := append([]Category(nil), ls.loc.Allowed...)
		cp.loc = ls.loc
		cp.loc.Allowed = allowed
		c.locations[id] = &cp
	}
	for id, p := range s.pallets {
		cp := *p
		c.pallets[id] = &cp
	}
	for id, loc := range s.where {
		c.where[id] = loc
	}
	for prod, pi := range s.products {
		ids := make(map[LocationID]struct{}, len(pi.ids))
		for id := range pi.ids {
			ids[id] = struct{}{}
		}
		c.products[prod] = &productIndex{ids: ids}
	}
	c.empty = newEmptyIndex(c.locations, c.pallets)
	return c
}
