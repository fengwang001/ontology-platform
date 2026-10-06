package slotting

import "sort"

// naiveModel 是独立编写的参考实现：不使用任何候选索引，
// 每次自动上架都对全部货位做全量扫描并朴素排序，
// 占用账以 map 直接维护。它与生产代码共享同一套规则描述，
// 但数据结构与实现路径完全不同，用于随机差分测试。
type naiveModel struct {
	locs    map[Coord]LocationConfig
	content map[Coord][]Pallet
	where   map[string]Coord
}

func newNaive(cfgs []LocationConfig) *naiveModel {
	m := &naiveModel{
		locs:    map[Coord]LocationConfig{},
		content: map[Coord][]Pallet{},
		where:   map[string]Coord{},
	}
	for _, c := range cfgs {
		cp := LocationConfig{
			Coord: c.Coord, WeightLimit: c.WeightLimit, ClearHeight: c.ClearHeight,
			Allowed: map[Category]bool{}, Capacity: c.Capacity,
			AllowMixedBatch: c.AllowMixedBatch, Status: c.Status,
		}
		for k, v := range c.Allowed {
			cp.Allowed[k] = v
		}
		m.locs[c.Coord] = cp
	}
	return m
}

func sortedCoords(cs []Coord) {
	sort.Slice(cs, func(i, j int) bool { return coordLess(cs[i], cs[j]) })
}

func (m *naiveModel) weight(c Coord) int {
	sum := 0
	for _, p := range m.content[c] {
		sum += p.Weight
	}
	return sum
}

// naiveCheck 按规则逐个检查，返回第一个命中的拒绝原因。
func (m *naiveModel) check(c Coord, p Pallet, ignorePallet string) Reason {
	cfg := m.locs[c]
	if cfg.Status == StatusFrozen {
		return ReasonFrozen
	}
	if !cfg.Allowed[p.Category] {
		return ReasonCategoryDenied
	}
	if p.Height > cfg.ClearHeight {
		return ReasonHeight
	}
	w := m.weight(c)
	if ignorePallet != "" {
		for _, q := range m.content[c] {
			if q.ID == ignorePallet {
				w -= q.Weight
			}
		}
	}
	if w+p.Weight > cfg.WeightLimit {
		return ReasonWeight
	}
	occ := m.content[c]
	for _, q := range occ {
		if q.ID == ignorePallet {
			continue
		}
		if q.Product != p.Product || (!cfg.AllowMixedBatch && q.Batch != p.Batch) {
			return ReasonMixConflict
		}
	}
	for _, nc := range neighborKeys(c) {
		if _, ok := m.locs[nc]; !ok {
			continue
		}
		for _, q := range m.content[nc] {
			if q.ID == ignorePallet {
				continue
			}
			if (p.Category == CategoryFlammable && q.Category == CategoryFood) ||
				(p.Category == CategoryFood && q.Category == CategoryFlammable) {
				return ReasonAdjacency
			}
		}
	}
	cnt := len(occ)
	if ignorePallet != "" {
		for _, q := range occ {
			if q.ID == ignorePallet {
				cnt--
			}
		}
	}
	if cnt >= cfg.Capacity {
		return ReasonCapacity
	}
	return ""
}

func (m *naiveModel) auto(p Pallet) (Coord, Reason) {
	// 阶段一：已存放同商品且有余位。
	productLocs := map[Coord]bool{}
	for c, ps := range m.content {
		if len(ps) == 0 {
			continue
		}
		if ps[0].Product == p.Product && len(ps) < m.locs[c].Capacity {
			productLocs[c] = true
		}
	}
	var first []Coord
	for c := range productLocs {
		if m.check(c, p, "") == "" {
			first = append(first, c)
		}
	}
	if len(first) > 0 {
		sortedCoords(first)
		return first[0], ""
	}
	// 阶段二：空货位。
	var second []Coord
	for c, cfg := range m.locs {
		if cfg.Status != StatusNormal || len(m.content[c]) != 0 {
			continue
		}
		if m.check(c, p, "") == "" {
			second = append(second, c)
		}
	}
	if len(second) > 0 {
		sortedCoords(second)
		return second[0], ""
	}
	return Coord{}, ReasonNoLocation
}

func (m *naiveModel) doAuto(p Pallet) (Coord, Reason) {
	if _, ok := m.where[p.ID]; ok {
		return Coord{}, ReasonDuplicate
	}
	c, r := m.auto(p)
	if r != "" {
		return c, r
	}
	m.content[c] = append(m.content[c], p)
	m.where[p.ID] = c
	return c, ""
}

func (m *naiveModel) doPut(p Pallet, target Coord) Reason {
	if _, ok := m.locs[target]; !ok {
		return ReasonLocationAbsent
	}
	if _, ok := m.where[p.ID]; ok {
		return ReasonDuplicate
	}
	if r := m.check(target, p, ""); r != "" {
		return r
	}
	m.content[target] = append(m.content[target], p)
	m.where[p.ID] = target
	return ""
}

func (m *naiveModel) doMove(id string, target Coord) Reason {
	src, ok := m.where[id]
	if !ok {
		return ReasonPalletMissing
	}
	if src == target {
		return ReasonSameLocation
	}
	if _, ok := m.locs[target]; !ok {
		return ReasonLocationAbsent
	}
	var p Pallet
	var ps []Pallet
	for _, q := range m.content[src] {
		if q.ID == id {
			p = q
		} else {
			ps = append(ps, q)
		}
	}
	if r := m.check(target, p, id); r != "" {
		return r
	}
	m.content[src] = ps
	m.content[target] = append(m.content[target], p)
	m.where[id] = target
	return ""
}

func (m *naiveModel) doRetrieve(id string) Reason {
	c, ok := m.where[id]
	if !ok {
		return ReasonPalletMissing
	}
	var ps []Pallet
	for _, q := range m.content[c] {
		if q.ID != id {
			ps = append(ps, q)
		}
	}
	m.content[c] = ps
	delete(m.where, id)
	return ""
}

func (m *naiveModel) doFreeze(c Coord, frozen bool) Reason {
	cfg, ok := m.locs[c]
	if !ok {
		return ReasonLocationAbsent
	}
	if frozen {
		cfg.Status = StatusFrozen
	} else {
		cfg.Status = StatusNormal
	}
	m.locs[c] = cfg
	return ""
}

// snapshotEqual 比较朴素模型与服务的全部占用状态。
func (m *naiveModel) dumpEqual(t interface{ Helper() }, svc *Service) bool {
	t.Helper()
	for c, cfg := range m.locs {
		v, err := svc.Location(c)
		if err != nil {
			return false
		}
		ps := m.content[c]
		if len(ps) != v.Occupied || m.weight(c) != cfg.WeightLimit-v.RemainingWeight {
			return false
		}
		got := map[string]bool{}
		for _, id := range v.PalletIDs {
			got[id] = true
		}
		for _, q := range ps {
			if !got[q.ID] {
				return false
			}
		}
	}
	for id, c := range m.where {
		gc, err := svc.PalletLocation(id)
		if err != nil || gc != c {
			return false
		}
	}
	return true
}
