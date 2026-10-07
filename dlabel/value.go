package dlabel

// ValueKind 标识属性值的类型。
type ValueKind int

const (
	KindNull ValueKind = iota
	KindInt
	KindFloat
	KindString
	KindBool
)

// Value 是属性的可空标量取值。
type Value struct {
	kind ValueKind
	i    int64
	f    float64
	s    string
	b    bool
}

func NullValue() Value           { return Value{kind: KindNull} }
func IntValue(v int64) Value     { return Value{kind: KindInt, i: v} }
func FloatValue(v float64) Value { return Value{kind: KindFloat, f: v} }
func StringValue(v string) Value { return Value{kind: KindString, s: v} }
func BoolValue(v bool) Value     { return Value{kind: KindBool, b: v} }

func (v Value) Kind() ValueKind { return v.kind }

func (v Value) Int() (int64, bool) {
	if v.kind != KindInt {
		return 0, false
	}
	return v.i, true
}

func (v Value) Float() (float64, bool) {
	switch v.kind {
	case KindFloat:
		return v.f, true
	case KindInt:
		return float64(v.i), true
	default:
		return 0, false
	}
}

func (v Value) String() (string, bool) {
	if v.kind != KindString {
		return "", false
	}
	return v.s, true
}

func (v Value) Bool() (bool, bool) {
	if v.kind != KindBool {
		return false, false
	}
	return v.b, true
}

// IsNull 报告取值是否为空。
func (v Value) IsNull() bool { return v.kind == KindNull }

// Equal 报告两个取值是否相等；不同类型不相等（空值之间相等）。
func (v Value) Equal(o Value) bool {
	if v.kind != o.kind {
		return false
	}
	switch v.kind {
	case KindNull:
		return true
	case KindInt:
		return v.i == o.i
	case KindFloat:
		return v.f == o.f
	case KindString:
		return v.s == o.s
	case KindBool:
		return v.b == o.b
	default:
		return false
	}
}

// compareValues 按算子比较两个取值。
//
// 类型规则（有意为之，保证规则求值无副作用且不产生额外错误通道）：
//   - Eq/Neq 对不同类型分别给出 false/true；
//   - Lt/Lte/Gt/Gte 仅在数值（int/float）或字符串同族时定义，
//     跨类型比较结果恒为 false（不抛错，避免错误通道泄露取值信息）；
//   - IsNull/NotNull 只依赖左值，右值忽略；
//   - 空值参与 Eq/Neq 之外的比较恒为 false。
func compareValues(a, b Value, op CompareOp) bool {
	switch op {
	case OpIsNull:
		return a.kind == KindNull
	case OpNotNull:
		return a.kind != KindNull
	case OpEq:
		return a.Equal(b)
	case OpNeq:
		return !a.Equal(b)
	}
	if a.kind == KindNull || b.kind == KindNull {
		return false
	}
	af, aok := a.Float()
	bf, bok := b.Float()
	if aok && bok {
		switch op {
		case OpLt:
			return af < bf
		case OpLte:
			return af <= bf
		case OpGt:
			return af > bf
		case OpGte:
			return af >= bf
		}
	}
	as, aok := a.String()
	bs, bok := b.String()
	if aok && bok {
		switch op {
		case OpLt:
			return as < bs
		case OpLte:
			return as <= bs
		case OpGt:
			return as > bs
		case OpGte:
			return as >= bs
		}
	}
	return false
}
