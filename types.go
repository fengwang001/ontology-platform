package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// Kind 区分属性值的类型。
type Kind int

const (
	KindInt Kind = iota
	KindText
)

// Value 是一个属性值。
type Value struct {
	Kind Kind
	Num  int64
	Text string
}

// Int 构造整数值。
func Int(v int64) Value { return Value{Kind: KindInt, Num: v} }

// Text 构造文本值。
func Text(s string) Value { return Value{Kind: KindText, Text: s} }

// Range 描述一个属性的合法取值范围。
type Range interface {
	// Contains 判断值是否落在范围内。
	Contains(v Value) bool
	// SubRangeOf 判断本范围是否为 other 的子范围（用于校验覆盖是否收窄）。
	SubRangeOf(other Range) bool
	String() string
}

// IntRange 是闭区间 [Min, Max] 的整数范围。
type IntRange struct {
	Min int64
	Max int64
}

// Contains 实现 Range。
func (r IntRange) Contains(v Value) bool {
	return v.Kind == KindInt && v.Num >= r.Min && v.Num <= r.Max
}

// SubRangeOf 实现 Range。
func (r IntRange) SubRangeOf(other Range) bool {
	o, ok := other.(IntRange)
	return ok && r.Min >= o.Min && r.Max <= o.Max
}

func (r IntRange) String() string { return fmt.Sprintf("int[%d,%d]", r.Min, r.Max) }

// StrSet 是枚举字符串集合范围。
type StrSet struct {
	Allowed []string
}

// Contains 实现 Range。
func (r StrSet) Contains(v Value) bool {
	if v.Kind != KindText {
		return false
	}
	for _, a := range r.Allowed {
		if a == v.Text {
			return true
		}
	}
	return false
}

// SubRangeOf 实现 Range。
func (r StrSet) SubRangeOf(other Range) bool {
	o, ok := other.(StrSet)
	if !ok {
		return false
	}
	allowed := make(map[string]bool, len(o.Allowed))
	for _, a := range o.Allowed {
		allowed[a] = true
	}
	for _, a := range r.Allowed {
		if !allowed[a] {
			return false
		}
	}
	return true
}

func (r StrSet) String() string {
	sorted := append([]string(nil), r.Allowed...)
	sort.Strings(sorted)
	return "enum{" + strings.Join(sorted, ",") + "}"
}
