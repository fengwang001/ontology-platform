package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// ObjectType 描述一个对象类型：父类型 ID（空串表示根类型）以及本类型
// 声明或覆盖的属性取值范围。属性的有效定义沿继承链取“最近声明”。
type ObjectType struct {
	ID     string
	Parent string
	Props  map[string]Range
}

// RuleVersion 是继承关系与属性覆盖规则的一个不可变版本。
// 版本在 [ValidFrom, 下一版本.ValidFrom) 内生效（左闭右开）。
type RuleVersion struct {
	ValidFrom int64
	Types     map[string]ObjectType
}

// Validate 校验版本内部一致性：父类型存在、无环、覆盖必须是对父链
// 已定义属性的收窄。
func (rv RuleVersion) Validate() error {
	ids := make([]string, 0, len(rv.Types))
	for id := range rv.Types {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := rv.Types[id]
		if t.ID != id {
			return fmt.Errorf("ontology: 类型键 %q 与其 ID %q 不一致", id, t.ID)
		}
		if t.Parent != "" {
			if _, ok := rv.Types[t.Parent]; !ok {
				return fmt.Errorf("ontology: 类型 %q 的父类型 %q 未定义", id, t.Parent)
			}
		}
		// 环检测：沿父链向上走，步数超过类型总数即成环。
		seen := map[string]bool{id: true}
		for cur := t.Parent; cur != ""; {
			if seen[cur] {
				return fmt.Errorf("ontology: 类型 %q 的继承链存在环", id)
			}
			seen[cur] = true
			cur = rv.Types[cur].Parent
		}
		// 覆盖校验：若父链已定义该属性，子类型的声明必须是收窄覆盖；
		// 若父链未定义，则视为子类型新增声明（退回父类型时该属性
		// 的取值将失去意义，重建时按规则丢弃）。
		if t.Parent != "" {
			props := make([]string, 0, len(t.Props))
			for p := range t.Props {
				props = append(props, p)
			}
			sort.Strings(props)
			for _, p := range props {
				parentRange, ok := rv.EffectiveRange(t.Parent, p)
				if ok && !t.Props[p].SubRangeOf(parentRange) {
					return fmt.Errorf("ontology: 类型 %q 对属性 %q 的覆盖 %v 未收窄父级范围 %v",
						id, p, t.Props[p], parentRange)
				}
			}
		}
	}
	return nil
}

// EffectiveRange 解析 typeID 的 prop 属性在该版本下的有效取值范围。
// 沿继承链自底向上，取最近（最细化）的声明。
func (rv RuleVersion) EffectiveRange(typeID, prop string) (Range, bool) {
	for cur := typeID; cur != ""; {
		t, ok := rv.Types[cur]
		if !ok {
			return nil, false
		}
		if r, ok := t.Props[prop]; ok {
			return r, true
		}
		cur = t.Parent
	}
	return nil, false
}

// IsAncestor 判断 anc 是否为 desc 的严格祖先。
func (rv RuleVersion) IsAncestor(anc, desc string) bool {
	for cur := desc; cur != ""; {
		t, ok := rv.Types[cur]
		if !ok {
			return false
		}
		cur = t.Parent
		if cur == anc {
			return true
		}
	}
	return false
}

// RuleStore 保存按生效时刻单调递增排列的规则版本，只允许追加。
type RuleStore struct {
	mu       sync.RWMutex
	versions []RuleVersion
}

// NewRuleStore 创建空规则库。
func NewRuleStore() *RuleStore { return &RuleStore{} }

// AddVersion 追加新版本。ValidFrom 必须严格大于此前所有版本。
// 版本一经追加即不可变，历史时刻的版本归属永不改变。
func (s *RuleStore) AddVersion(v RuleVersion) error {
	if err := v.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.versions); n > 0 && v.ValidFrom <= s.versions[n-1].ValidFrom {
		return fmt.Errorf("ontology: 规则版本生效时刻 %d 未严格大于前一版本 %d",
			v.ValidFrom, s.versions[n-1].ValidFrom)
	}
	s.versions = append(s.versions, v)
	return nil
}

// EffectiveAt 返回时刻 t 生效的版本及其序号；t 早于首个版本时返回 nil, -1。
// 边界规则：t == ValidFrom 时归属新版本（左闭区间）。
func (s *RuleStore) EffectiveAt(t int64) (*RuleVersion, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 二分查找最后一个 ValidFrom <= t 的版本。
	idx := sort.Search(len(s.versions), func(i int) bool {
		return s.versions[i].ValidFrom > t
	}) - 1
	if idx < 0 {
		return nil, -1
	}
	return &s.versions[idx], idx
}

// Versions 返回全部版本的拷贝（用于审计与测试记录）。
func (s *RuleStore) Versions() []RuleVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RuleVersion, len(s.versions))
	copy(out, s.versions)
	return out
}
