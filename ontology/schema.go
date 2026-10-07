package ontologyindex

import "sort"

// LogicalClock 是逻辑时间戳（单调、全局有序）。
// 合法串行顺序唯一由变更事件携带的逻辑时间戳决定，与物理到达时刻无关。
type LogicalClock int64

// Value 是可比较、可哈希的属性取值（仅支持基础标量，作为索引键）。
type Value struct {
	kind  byte
	num   int64
	str   string
	valid bool
}

const (
	valueNull byte = 0
	valueInt  byte = 1
	valueStr  byte = 2
)

// IntValue 构造整数值。
func IntValue(n int64) Value { return Value{kind: valueInt, num: n, valid: true} }

// StringValue 构造字符串值。
func StringValue(s string) Value { return Value{kind: valueStr, str: s, valid: true} }

// IndexConstraint 约束新字段作为索引依据时必须满足的条件。
type IndexConstraint int

const (
	// ConstraintDuplicate 允许不同对象取相同值（普通倒排）。
	ConstraintDuplicate IndexConstraint = iota
	// ConstraintUnique 要求每个取值至多对应一个对象（唯一索引）。
	ConstraintUnique
)

// FieldVersion 描述某一类型版本中一个物理字段的定义。
type FieldVersion struct {
	// Physical 是该版本中的物理字段名（可能随迁移改变）。
	Physical string
	// PropertyID 是跨版本稳定的逻辑属性身份；字段改名/替换时保持不变，
	// 迁移废弃（无替代）时该属性链终止。
	PropertyID string
	// Deprecated 表示该版本中该属性已废弃。
	Deprecated bool
	// ReplacedBy 是替代属性的 PropertyID；空表示无替代。
	ReplacedBy string
}

// TypeVersion 是对象类型在某一生效区间 [EffectiveAt, 下一版本生效时刻) 的定义。
type TypeVersion struct {
	EffectiveAt LogicalClock
	Fields      []FieldVersion
}

// ObjectType 是一个对象类型的完整版本时间线。
type ObjectType struct {
	Name     string
	Versions []TypeVersion
}

// Schema 是全部对象类型的定义注册表。
type Schema struct {
	types map[string]*ObjectType
}

// NewSchema 创建空注册表。
func NewSchema() *Schema { return &Schema{types: map[string]*ObjectType{}} }

// AddType 注册（或整体替换）一个对象类型的版本时间线。
func (s *Schema) AddType(t *ObjectType) {
	versions := append([]TypeVersion(nil), t.Versions...)
	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].EffectiveAt < versions[j].EffectiveAt
	})
	cp := *t
	cp.Versions = versions
	s.types[t.Name] = &cp
}

// versionAt 返回在逻辑时刻 t 生效的类型版本。
func (s *Schema) versionAt(typeName string, t LogicalClock) (*TypeVersion, bool) {
	ot, ok := s.types[typeName]
	if !ok {
		return nil, false
	}
	var chosen *TypeVersion
	for i := range ot.Versions {
		v := &ot.Versions[i]
		if v.EffectiveAt <= t {
			chosen = v
		} else {
			break
		}
	}
	return chosen, chosen != nil
}

// resolveField 返回事件在逻辑时刻 t 所引用属性解析到的字段定义。
func (s *Schema) resolveField(typeName, propertyID string, t LogicalClock) (FieldVersion, *TypeVersion, bool) {
	v, ok := s.versionAt(typeName, t)
	if !ok {
		return FieldVersion{}, nil, false
	}
	for _, f := range v.Fields {
		if f.PropertyID == propertyID {
			return f, v, true
		}
	}
	return FieldVersion{}, v, false
}

// resolveProperty 在类型于时刻 t 生效的版本中解析逻辑属性，沿
// “已废弃→替代字段”链追踪到当前物理字段。链终止（废弃且无替代）
// 返回 ErrDeprecatedProperty；版本中不存在该属性返回 ErrPropertyUndefined。
func (s *Schema) resolveProperty(typeName, propertyID string, t LogicalClock) (FieldVersion, error) {
	seen := map[string]struct{}{}
	current := propertyID
	for {
		if _, dup := seen[current]; dup {
			return FieldVersion{}, errDeprecated("property %q 替代链存在环", propertyID)
		}
		seen[current] = struct{}{}
		f, v, ok := s.resolveField(typeName, current, t)
		if !ok {
			if v == nil {
				return FieldVersion{}, errUndefined("类型 %q 在逻辑时刻 %d 尚无版本定义", typeName, t)
			}
			return FieldVersion{}, errUndefined("类型 %q 在逻辑时刻 %d 生效的版本未定义属性 %q", typeName, t, current)
		}
		if !f.Deprecated {
			return f, nil
		}
		if f.ReplacedBy == "" {
			return FieldVersion{}, errDeprecated("属性 %q 已在类型 %q 的定义迁移中废弃且未指定替代字段", current, typeName)
		}
		current = f.ReplacedBy
	}
}

// lineageOf 返回某逻辑属性在整个版本时间线上出现过的全部属性身份
// （含沿替代链可达的属性），用于把事件路由到正确的索引。
func (s *Schema) lineageOf(typeName, propertyID string) map[string]struct{} {
	out := map[string]struct{}{}
	ot, ok := s.types[typeName]
	if !ok {
		out[propertyID] = struct{}{}
		return out
	}
	var walk func(pid string)
	walk = func(pid string) {
		if _, ok := out[pid]; ok {
			return
		}
		out[pid] = struct{}{}
		for i := range ot.Versions {
			for _, f := range ot.Versions[i].Fields {
				if f.PropertyID == pid && f.Deprecated && f.ReplacedBy != "" {
					walk(f.ReplacedBy)
				}
			}
		}
	}
	walk(propertyID)
	return out
}
