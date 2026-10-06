// Package store 维护合同集合与“线路+等级+承运商”维度的时间区间索引。
//
// 本包自身不加锁；并发串行化由上层 system 以单把 RWMutex 保证，
// 因而“并发结果等价于某个串行顺序”可直接由锁的语义证明。
package store

import (
	"sort"

	"ontology/freight/model"
)

// laneKey 定位一组同承运商、同线路、同等级的合同。
type laneKey struct {
	carrier string
	from    model.Region
	to      model.Region
	class   model.ServiceClass
}

type routeKey struct {
	from  model.Region
	to    model.Region
	class model.ServiceClass
}

type lane struct {
	contracts []*model.Contract // 按 EffectiveAt 升序，区间两两不重叠
}

// Store 合同存储与区间索引。
type Store struct {
	byID       map[string]*model.Contract
	laneID     map[laneKey]*lane
	routeLanes map[routeKey]map[string]struct{}

	// probe 统计 Find 二分过程中实际比较过的合同份数，
	// 供复杂度证明测试断言其与车道合同总数无关（对数级）。
	probe int
}

// New 创建空存储。
func New() *Store {
	return &Store{
		byID:       map[string]*model.Contract{},
		laneID:     map[laneKey]*lane{},
		routeLanes: map[routeKey]map[string]struct{}{},
	}
}

func (s *Store) laneKeyOf(c *model.Contract) laneKey {
	return laneKey{carrier: c.CarrierID, from: c.Route.From, to: c.Route.To, class: c.Class}
}

func (s *Store) laneFor(carrier string, r model.Route, class model.ServiceClass) *lane {
	return s.laneID[laneKey{carrier: carrier, from: r.From, to: r.To, class: class}]
}

// overlaps 判断两个左闭右开区间是否重叠（相接不算重叠）。
func overlaps(aLo, aHi, bLo, bHi model.Time) bool {
	return aLo < bHi && bLo < aHi
}

// cloneContract 深拷贝合同并对偏远清单排序去重，保证库内对象与调用方解耦。
func cloneContract(c *model.Contract) *model.Contract {
	cp := *c
	cp.Tiers = append([]model.WeightTier(nil), c.Tiers...)
	regions := append([]model.Region(nil), c.RemoteRegions...)
	sort.Slice(regions, func(i, j int) bool { return regions[i] < regions[j] })
	keep := regions[:0]
	for i, r := range regions {
		if i > 0 && r == regions[i-1] {
			continue
		}
		keep = append(keep, r)
	}
	cp.RemoteRegions = keep
	return &cp
}

// Add 新增或按 ID 修订合同（同 ID 替换）。要求合同已通过参数校验。
// 若与同车道其它合同的生效区间重叠（相接允许），返回 model.CodeIntervalOverlap，
// 且不改变任何已有合同。修订不得改变承运商、线路或等级。
func (s *Store) Add(c *model.Contract) error {
	k := s.laneKeyOf(c)
	ln := s.laneID[k]

	if existing := s.byID[c.ID]; existing != nil {
		if existing.CarrierID != c.CarrierID ||
			existing.Route != c.Route || existing.Class != c.Class {
			return model.ErrInvalid("修订合同不得改变承运商、线路或等级: %s", c.ID)
		}
	}

	// 构造“若修订生效”的候选有序合同列表，并在其上做重叠校验。
	capacity := 1
	if ln != nil {
		capacity = len(ln.contracts) + 1
	}
	tmp := make([]*model.Contract, 0, capacity)
	if ln != nil {
		for _, cur := range ln.contracts {
			if cur.ID != c.ID {
				tmp = append(tmp, cur)
			}
		}
	}
	for _, cur := range tmp {
		if overlaps(c.EffectiveAt, c.ExpiresAt, cur.EffectiveAt, cur.ExpiresAt) {
			return model.ErrIntervalOverlap(
				"合同 %s 的区间 [%d,%d) 与已有合同 %s 的区间 [%d,%d) 重叠",
				c.ID, c.EffectiveAt, c.ExpiresAt,
				cur.ID, cur.EffectiveAt, cur.ExpiresAt)
		}
	}

	// 校验全部通过后才提交，保证失败零副作用。
	nc := cloneContract(c)
	insertAt := sort.Search(len(tmp), func(i int) bool {
		return nc.EffectiveAt < tmp[i].EffectiveAt
	})
	tmp = append(tmp, nil)
	copy(tmp[insertAt+1:], tmp[insertAt:])
	tmp[insertAt] = nc
	if ln == nil {
		ln = &lane{}
		s.laneID[k] = ln
	}
	ln.contracts = tmp

	s.byID[c.ID] = nc
	rk := routeKey{from: c.Route.From, to: c.Route.To, class: c.Class}
	if s.routeLanes[rk] == nil {
		s.routeLanes[rk] = map[string]struct{}{}
	}
	s.routeLanes[rk][c.CarrierID] = struct{}{}
	return nil
}

// Find 返回指定承运商/线路/等级在时刻 t 生效的合同；不存在返回 nil。
// 复杂度 O(log k)，k 为该车道合同份数；比较次数计入 probe。
func (s *Store) Find(carrier string, r model.Route, class model.ServiceClass, t model.Time) *model.Contract {
	ln := s.laneFor(carrier, r, class)
	if ln == nil {
		return nil
	}
	// 二分：找最后一份 EffectiveAt <= t 的合同。
	idx := sort.Search(len(ln.contracts), func(i int) bool {
		s.probe++
		return ln.contracts[i].EffectiveAt > t
	})
	if idx == 0 {
		return nil
	}
	cand := ln.contracts[idx-1]
	if t < cand.ExpiresAt {
		return cand
	}
	return nil
}

// LaneExists 报告该承运商的该线路/等级下是否存在任何合同（不论时刻）。
func (s *Store) LaneExists(carrier string, r model.Route, class model.ServiceClass) bool {
	ln := s.laneFor(carrier, r, class)
	return ln != nil && len(ln.contracts) > 0
}

// Carriers 返回某线路/等级下登记过合同的全部承运商（按编号升序）。
func (s *Store) Carriers(r model.Route, class model.ServiceClass) []string {
	set := s.routeLanes[routeKey{from: r.From, to: r.To, class: class}]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ProbeCount 返回自上次重置以来 Find 比较过的合同份数。
func (s *Store) ProbeCount() int { return s.probe }

// ResetProbe 清零比较计数。
func (s *Store) ResetProbe() { s.probe = 0 }
