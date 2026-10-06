package underwriting

import (
	"maps"
	"slices"
	"sync"
)

// validate 校验规则参数合法性。
func (r Rule) validate() error {
	if r.ID == "" {
		return ErrInvalidParam
	}
	if r.Start < 0 || r.Start >= r.End {
		return ErrInvalidParam
	}
	c := r.Cond
	if c.MinAge != nil && *c.MinAge < 0 {
		return ErrInvalidParam
	}
	if c.MaxAge != nil && *c.MaxAge < 0 {
		return ErrInvalidParam
	}
	if c.MinAge != nil && c.MaxAge != nil && *c.MinAge > *c.MaxAge {
		return ErrInvalidParam
	}
	for occ := range c.Occupations {
		if occ < 1 || occ > 6 {
			return ErrInvalidParam
		}
	}
	switch r.Action.Kind {
	case ActionStandard, ActionDecline:
	case ActionExtraPremium:
		if r.Action.Percent <= 0 {
			return ErrInvalidParam
		}
	case ActionExclusion:
		if r.Action.ExclusionCode == "" {
			return ErrInvalidParam
		}
	case ActionPostpone:
		if r.Action.PostponeUntil < 0 {
			return ErrInvalidParam
		}
	default:
		return ErrInvalidParam
	}
	return nil
}

// RuleStore 是管理员维护的规则库，按编号互斥增删改，并发安全。
type RuleStore struct {
	mu    sync.RWMutex
	rules map[string]Rule
}

func NewRuleStore() *RuleStore {
	return &RuleStore{rules: make(map[string]Rule)}
}

func (s *RuleStore) Add(r Rule) error {
	if err := r.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[r.ID]; ok {
		return ErrRuleDuplicate
	}
	s.rules[r.ID] = r
	return nil
}

func (s *RuleStore) Update(r Rule) error {
	if err := r.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[r.ID]; !ok {
		return ErrRuleNotFound
	}
	s.rules[r.ID] = r
	return nil
}

func (s *RuleStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return ErrRuleNotFound
	}
	delete(s.rules, id)
	return nil
}

// Snapshot 返回当前规则库的不可变索引快照；之后的增删改不影响它。
func (s *RuleStore) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return newSnapshot(maps.Clone(s.rules))
}

// Snapshot 是某时刻规则库的不可变快照，按职业类别分桶、桶内按年龄区间建索引。
// 单次裁定只访问与投保单职业类别相关（含不限职业）且覆盖其年龄的规则，
// 与年龄或职业类别都不相关的规则数量不影响裁定开销。
type Snapshot struct {
	rules   map[string]Rule
	buckets [7]ageIndex // 0 为不限职业的桶，1..6 为各职业类别
}

func newSnapshot(rules map[string]Rule) *Snapshot {
	snap := &Snapshot{rules: rules}
	var buckets [7][]Rule
	for _, r := range rules {
		if len(r.Cond.Occupations) == 0 {
			buckets[0] = append(buckets[0], r)
			continue
		}
		for occ := range r.Cond.Occupations {
			buckets[occ] = append(buckets[occ], r)
		}
	}
	for i := range snap.buckets {
		snap.buckets[i] = buildAgeIndex(buckets[i])
	}
	return snap
}

// candidates 返回覆盖 (age, occupation) 的候选规则；其它规则不被触及。
func (s *Snapshot) candidates(age int64, occupation int) []Rule {
	out := s.buckets[0].query(age)
	out = append(out, s.buckets[occupation].query(age)...)
	return out
}

// ageIndex 是静态区间刺探索引：把规则年龄区间的端点离散化后，
// 每个基本分段预计算覆盖它的规则列表，查询为一次二分。
type ageIndex struct {
	points []int64  // 递增的分段端点
	seg    [][]Rule // seg[i] 覆盖 [points[i], points[i+1])，末段覆盖 [points[last], +∞)
}

func buildAgeIndex(rules []Rule) ageIndex {
	if len(rules) == 0 {
		return ageIndex{}
	}
	type bound struct{ lo, hi int64 } // hi 为开区间端点，负数表示 +∞
	bounds := make([]bound, 0, len(rules))
	pointSet := map[int64]bool{}
	for _, r := range rules {
		var lo, hi int64 = 0, -1
		if r.Cond.MinAge != nil {
			lo = int64(*r.Cond.MinAge)
		}
		if r.Cond.MaxAge != nil {
			hi = int64(*r.Cond.MaxAge) + 1
		}
		bounds = append(bounds, bound{lo, hi})
		pointSet[lo] = true
		if hi >= 0 {
			pointSet[hi] = true
		}
	}
	points := make([]int64, 0, len(pointSet))
	for p := range pointSet {
		points = append(points, p)
	}
	slices.Sort(points)
	seg := make([][]Rule, len(points))
	for i, p := range points {
		for j, b := range bounds {
			if b.lo <= p && (b.hi < 0 || p < b.hi) {
				seg[i] = append(seg[i], rules[j])
			}
		}
	}
	return ageIndex{points: points, seg: seg}
}

func (ix ageIndex) query(age int64) []Rule {
	i, ok := slices.BinarySearch(ix.points, age)
	if !ok {
		i-- // 最后一个 <= age 的端点
	}
	if i < 0 {
		return nil
	}
	return ix.seg[i]
}
