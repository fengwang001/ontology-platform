package ontology

import (
	"strconv"
)

// PropertyType 是属性取值类型的标识。
type PropertyType string

const (
	TypeString PropertyType = "string"
	TypeInt    PropertyType = "int"
	TypeFloat  PropertyType = "float"
	TypeBool   PropertyType = "bool"
)

// Valid 报告类型标识本身是否合法。
func (t PropertyType) Valid() bool {
	switch t {
	case TypeString, TypeInt, TypeFloat, TypeBool:
		return true
	}
	return false
}

// Value 是一个带类型标签的标量值，用于实例属性取值与主键取值。
// 使用显式 Kind 标签，避免 JSON 编码时 int 3 与 float 3.0 混淆。
type Value struct {
	Kind PropertyType `json:"k"`
	Str  string       `json:"s,omitempty"`
	Int  int64        `json:"i,omitempty"`
	Flt  float64      `json:"f,omitempty"`
	Bol  bool         `json:"b,omitempty"`
}

func StringValue(s string) Value { return Value{Kind: TypeString, Str: s} }
func IntValue(i int64) Value     { return Value{Kind: TypeInt, Int: i} }
func FloatValue(f float64) Value { return Value{Kind: TypeFloat, Flt: f} }
func BoolValue(b bool) Value     { return Value{Kind: TypeBool, Bol: b} }

func (v Value) numeric() bool { return v.Kind == TypeInt || v.Kind == TypeFloat }

func (v Value) asFloat() float64 {
	if v.Kind == TypeInt {
		return float64(v.Int)
	}
	return v.Flt
}

// ValidFor 报告该值按类型 t 解读是否有效。
// 唯一允许的隐式兼容是 int 值可以按 float 解读（拓宽方向）。
func (v Value) ValidFor(t PropertyType) bool {
	switch t {
	case TypeString:
		return v.Kind == TypeString
	case TypeInt:
		return v.Kind == TypeInt
	case TypeFloat:
		return v.Kind == TypeInt || v.Kind == TypeFloat
	case TypeBool:
		return v.Kind == TypeBool
	}
	return false
}

// EqualValues 比较两个值是否相等。数值之间按数值比较
// （int 3 与 float 3.0 相等），其余情况要求 Kind 一致。
func EqualValues(a, b Value) bool {
	if a.Kind == b.Kind {
		switch a.Kind {
		case TypeString:
			return a.Str == b.Str
		case TypeInt:
			return a.Int == b.Int
		case TypeFloat:
			return a.Flt == b.Flt
		case TypeBool:
			return a.Bol == b.Bol
		}
		return true
	}
	if a.numeric() && b.numeric() {
		return a.asFloat() == b.asFloat()
	}
	return false
}

// key 返回值的规范字符串编码，用于对象主键与链接端点的身份标识。
func (v Value) key() string {
	switch v.Kind {
	case TypeString:
		return "s\x1f" + v.Str
	case TypeInt:
		return "i\x1f" + strconv.FormatInt(v.Int, 10)
	case TypeFloat:
		return "f\x1f" + strconv.FormatFloat(v.Flt, 'g', -1, 64)
	case TypeBool:
		return "b\x1f" + strconv.FormatBool(v.Bol)
	}
	return "?\x1f"
}

// RetypeClass 描述一次属性类型调整的方向。
type RetypeClass string

const (
	RetypeWidening  RetypeClass = "widening"
	RetypeNarrowing RetypeClass = "narrowing"
)

// ClassifyRetype 对一次类型变化分类：int→float 为拓宽，其余为收紧。
// 调用方保证 old != new。
func ClassifyRetype(oldT, newT PropertyType) RetypeClass {
	if oldT == TypeInt && newT == TypeFloat {
		return RetypeWidening
	}
	return RetypeNarrowing
}
