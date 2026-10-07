package ontology

// ValueKind 区分属性值的类型。
type ValueKind int

const (
	IntValue ValueKind = iota
	StringValue
)

// Value 是一个属性值。
type Value struct {
	Kind ValueKind
	Int  int64
	Str  string
}

// IntVal 构造整数值。
func IntVal(i int64) Value { return Value{Kind: IntValue, Int: i} }

// StrVal 构造字符串值。
func StrVal(s string) Value { return Value{Kind: StringValue, Str: s} }

// RangeKind 区分取值范围的形态。
type RangeKind int

const (
	// AnyRange 接受任意值。
	AnyRange RangeKind = iota
	// IntRangeKind 接受 [Min, Max] 内的整数。
	IntRangeKind
	// EnumRangeKind 接受枚举集合内的字符串。
	EnumRangeKind
)

// ValueRange 描述一个属性的取值范围。
type ValueRange struct {
	Kind RangeKind
	Min  int64
	Max  int64
	Enum []string
}

// Contains 判断 v 是否落在范围内。
func (r ValueRange) Contains(v Value) bool {
	switch r.Kind {
	case AnyRange:
		return true
	case IntRangeKind:
		return v.Kind == IntValue && v.Int >= r.Min && v.Int <= r.Max
	case EnumRangeKind:
		if v.Kind != StringValue {
			return false
		}
		for _, s := range r.Enum {
			if s == v.Str {
				return true
			}
		}
		return false
	}
	return false
}
