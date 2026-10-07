package compat

import "math"

// Kind 是属性取值的基础类型类别。
type Kind int

const (
	KindString Kind = iota
	KindInteger
	KindFloat
	KindBoolean
	KindEnum
)

// TypeSpec 描述一个属性允许的取值集合。
//
//   - KindString / KindBoolean：无额外约束；
//   - KindInteger / KindFloat ：Min、Max 为可选闭区间边界，nil 表示无界；
//   - KindEnum：Enum 列出全部允许的字面量。
type TypeSpec struct {
	Kind Kind
	Enum []string
	Min  *float64
	Max  *float64
}

// Property  是对象类型的一个属性定义。
type Property struct {
	Type     TypeSpec
	Required bool
}

// ObjectType 是对象类型：属性名 -> 属性定义。
type ObjectType map[string]Property

// Schema 是某一格式版本下的完整对象类型结构：对象类型名 -> 对象类型。
type Schema map[string]ObjectType

// Contains 报告取值 v 是否落在 t 允许的范围内。
func (t TypeSpec) Contains(v any) bool {
	switch t.Kind {
	case KindString:
		_, ok := v.(string)
		return ok
	case KindBoolean:
		_, ok := v.(bool)
		return ok
	case KindInteger:
		f, ok := asFloat(v)
		if !ok || f != math.Trunc(f) {
			return false
		}
		return t.inRange(f)
	case KindFloat:
		f, ok := asFloat(v)
		return ok && t.inRange(f)
	case KindEnum:
		s, ok := v.(string)
		if !ok {
			return false
		}
		for _, e := range t.Enum {
			if e == s {
				return true
			}
		}
		return false
	}
	return false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func (t TypeSpec) inRange(f float64) bool {
	if t.Min != nil && f < *t.Min {
		return false
	}
	if t.Max != nil && f > *t.Max {
		return false
	}
	return true
}

// SubsetOf 报告 a 允许的全部取值是否都落在 b 允许的范围内。
// 用于判定取值类型收紧/放宽：a ⊆ b 表示从 a 到 b 是放宽（或不变）。
func SubsetOf(a, b TypeSpec) bool {
	if a.Kind == b.Kind {
		switch a.Kind {
		case KindInteger, KindFloat:
			return boundsWithin(a, b)
		case KindEnum:
			return enumSubset(a.Enum, b.Enum)
		default:
			return true
		}
	}
	// 整数是有理数的子集：Integer ⊆ Float 当且仅当边界相容。
	if a.Kind == KindInteger && b.Kind == KindFloat {
		return boundsWithin(a, b)
	}
	return false
}

func boundsWithin(a, b TypeSpec) bool {
	if b.Min != nil && (a.Min == nil || *a.Min < *b.Min) {
		return false
	}
	if b.Max != nil && (a.Max == nil || *a.Max > *b.Max) {
		return false
	}
	return true
}

func enumSubset(a, b []string) bool {
	set := make(map[string]bool, len(b))
	for _, s := range b {
		set[s] = true
	}
	for _, s := range a {
		if !set[s] {
			return false
		}
	}
	return true
}

// FloatPtr 返回 f 的指针，便于构造字面量。
func FloatPtr(f float64) *float64 { return &f }
