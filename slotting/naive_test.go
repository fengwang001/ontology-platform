package slotting

// naiveModel 是与实现完全独立编写的朴素模型：
// 不用任何索引，每次自动上架全量线性扫描；作为随机差分测试的对照。

type naiveLoc struct {
	loc     Location
	pallets []string
	weight  int
}

type naiveModel struct {
	locs    map[LocationID]*naiveLoc
	pallets map[string]*Pallet
	where   map[string]LocationID
}

func newNaive() *naiveModel {
	return &naiveModel{
		locs:    map[LocationID]*naiveLoc{},
		pallets: map[string]*Pallet{},
		where:   map[string]LocationID{},
	}
}

func (m *naiveModel) add(loc Location) {
	m.locs[loc.ID] = &naiveLoc{loc: loc}
}

func (m *naiveModel) reg(p Pallet) {
	m.pallets[p.ID] = &p
}

func (m *naiveModel) sortedIDs() []LocationID {
	ids := make([]LocationID, 0, len(m.locs))
	for id := range m.locs {
		ids = append(ids, id)
	}
	sortIDs(ids)
	return ids
}

func nCats(allowed []Category, c Category) bool {
	for _, a := range allowed {
		if a == c {
			return true
		}
	}
	return false
}

func nConflict(a, b Category) bool {
	return a == CatFlammable && b == CatFood || a == CatFood && b == CatFlammable
}

// feasible 判定 p 能否放入 ls（在 m 的当前状态下）。
func (m *naiveModel) feasible(ls *naiveLoc, p *Pallet, frozenBlocks bool) Reason {
	if frozenBlocks && ls.loc.Status == StatusFrozen {
		return ReasonFrozen
	}
	if !nCats(ls.loc.Allowed, p.Category) {
		return ReasonCategoryNotAllowed
	}
	if p.Height > ls.loc.ClearHeight {
		return ReasonHeightInsufficient
	}
	if ls.weight+p.Weight > ls.loc.MaxWeight {
		return ReasonWeightInsufficient
	}
	if len(ls.pallets) > 0 {
		first := m.pallets[ls.pallets[0]]
		if first.Product != p.Product {
			return ReasonMixConflict
		}
		if !ls.loc.AllowMix {
			for _, pid := range ls.pallets {
				if m.pallets[pid].Batch != p.Batch {
					return ReasonMixConflict
				}
			}
		}
	}
	for _, d := range []int{-1, 1} {
		nid := LocationID{ls.loc.ID.Aisle, ls.loc.ID.Level, ls.loc.ID.Index + d}
		ns := m.locs[nid]
		if ns == nil {
			continue
		}
		for _, npid := range ns.pallets {
			if nConflict(p.Category, m.pallets[npid].Category) {
				return ReasonAdjacencyConflict
			}
		}
	}
	if len(ls.pallets) >= ls.loc.Capacity {
		return ReasonCapacityFull
	}
	return ""
}

func (m *naiveModel) place(id LocationID, p *Pallet) {
	ls := m.locs[id]
	ls.pallets = append(ls.pallets, p.ID)
	ls.weight += p.Weight
	m.where[p.ID] = id
}

func (m *naiveModel) release(id LocationID, pid string) {
	ls := m.locs[id]
	out := ls.pallets[:0]
	for _, x := range ls.pallets {
		if x != pid {
			out = append(out, x)
		}
	}
	ls.pallets = out
	ls.weight -= m.pallets[pid].Weight
	delete(m.where, pid)
}

// auto 返回 (货位, 是否成功)，完全按题面次序线性扫描。
func (m *naiveModel) auto(pid string) (LocationID, bool) {
	p := m.pallets[pid]
	ids := m.sortedIDs()
	// 第一类：同商品且有剩余容量。
	for _, id := range ids {
		ls := m.locs[id]
		if len(ls.pallets) == 0 || len(ls.pallets) >= ls.loc.Capacity {
			continue
		}
		if m.pallets[ls.pallets[0]].Product != p.Product {
			continue
		}
		// 第一类候选同样必须通过完整约束（含相邻隔离）。
		if r := m.feasible(ls, p, true); r == "" {
			m.place(id, p)
			return id, true
		}
	}
	// 第二类：空货位。
	for _, id := range ids {
		ls := m.locs[id]
		if len(ls.pallets) != 0 {
			continue
		}
		if m.feasible(ls, p, true) == "" {
			m.place(id, p)
			return id, true
		}
	}
	return LocationID{}, false
}

// placeTo 返回拒绝原因（""表示成功），按题面优先级。
func (m *naiveModel) placeTo(pid string, t LocationID) Reason {
	p := m.pallets[pid]
	ls, ok := m.locs[t]
	if !ok {
		return ReasonLocationNotFound
	}
	if _, on := m.where[pid]; on {
		return ReasonDuplicate
	}
	if r := m.feasible(ls, p, true); r != "" {
		return r
	}
	m.place(t, p)
	return ""
}

func (m *naiveModel) move(pid string, t LocationID) Reason {
	origin, on := m.where[pid]
	if !on {
		return ReasonPalletNotFound
	}
	if origin == t {
		return ReasonSelfLocation
	}
	if _, ok := m.locs[t]; !ok {
		return ReasonLocationNotFound
	}
	// 快照式：复制全部在位关系后模拟。
	saved := *m
	savedLocs := map[LocationID]*naiveLoc{}
	for id, ls := range m.locs {
		c := &naiveLoc{loc: ls.loc, weight: ls.weight, pallets: append([]string(nil), ls.pallets...)}
		savedLocs[id] = c
	}
	savedWhere := map[string]LocationID{}
	for k, v := range m.where {
		savedWhere[k] = v
	}
	m.release(origin, pid)
	if r := m.feasible(m.locs[t], m.pallets[pid], true); r != "" {
		m.locs = savedLocs
		m.where = savedWhere
		return r
	}
	m.place(t, m.pallets[pid])
	_ = saved
	return ""
}

func (m *naiveModel) take(pid string) bool {
	id, ok := m.where[pid]
	if !ok {
		return false
	}
	m.release(id, pid)
	return true
}

func (m *naiveModel) freeze(id LocationID, frozen bool) bool {
	ls, ok := m.locs[id]
	if !ok {
		return false
	}
	if frozen {
		ls.loc.Status = StatusFrozen
	} else {
		ls.loc.Status = StatusNormal
	}
	return true
}
