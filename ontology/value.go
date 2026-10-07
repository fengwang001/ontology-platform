package ontology

// Kind 是字段取值类型的标识。
type Kind int

const (
	KindInt Kind = iota
	KindFloat
	KindString
	KindBool
)

func (k Kind) String() string {
	switch k {
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindBool:
		return "bool"
	}
	return "unknown"
}

// Value 是一个字段取值。同一时间只有与 Kind 对应的成员有效。
type Value struct {
	Kind Kind
	I    int64
	F    float64
	S    string
	B    bool
}

func IntValue(v int64) Value     { return Value{Kind: KindInt, I: v} }
func FloatValue(v float64) Value { return Value{Kind: KindFloat, F: v} }
func StringValue(v string) Value { return Value{Kind: KindString, S: v} }
func BoolValue(v bool) Value     { return Value{Kind: KindBool, B: v} }

// Equal 是精确相等判定，不做任何近似比较。
func (v Value) Equal(o Value) bool {
	if v.Kind != o.Kind {
		return false
	}
	switch v.Kind {
	case KindInt:
		return v.I == o.I
	case KindFloat:
		return v.F == o.F
	case KindString:
		return v.S == o.S
	case KindBool:
		return v.B == o.B
	}
	return false
}

// ValueType 声明一个字段取值类型。无损重解释规则由类型自身声明，
// 判定必须精确，不允许近似比较。
type ValueType interface {
	Kind() Kind
	// ReinterpretTo 尝试把本类型下的取值 v 无损地重新解释为目标类型
	// 下的一个合法取值。ok=false 表示无法无损转换。
	ReinterpretTo(target ValueType, v Value) (nv Value, ok bool)
}

var (
	IntType    ValueType = intType{}
	FloatType  ValueType = floatType{}
	StringType ValueType = stringType{}
	BoolType   ValueType = boolType{}
)

type intType struct{}

func (intType) Kind() Kind { return KindInt }

func (intType) ReinterpretTo(target ValueType, v Value) (Value, bool) {
	switch target.Kind() {
	case KindInt:
		return v, true
	case KindFloat:
		// 仅当 int64 -> float64 -> int64 精确往返时无损。
		f := float64(v.I)
		if int64(f) != v.I {
			return Value{}, false
		}
		return FloatValue(f), true
	}
	return Value{}, false
}

type floatType struct{}

func (floatType) Kind() Kind { return KindFloat }

func (floatType) ReinterpretTo(target ValueType, v Value) (Value, bool) {
	switch target.Kind() {
	case KindFloat:
		return v, true
	case KindInt:
		// 仅当值为整数且在 int64 范围内精确往返时无损。
		i := int64(v.F)
		if float64(i) != v.F {
			return Value{}, false
		}
		return IntValue(i), true
	}
	return Value{}, false
}

type stringType struct{}

func (stringType) Kind() Kind { return KindString }

func (stringType) ReinterpretTo(target ValueType, v Value) (Value, bool) {
	if target.Kind() == KindString {
		return v, true
	}
	return Value{}, false
}

type boolType struct{}

func (boolType) Kind() Kind { return KindBool }

func (boolType) ReinterpretTo(target ValueType, v Value) (Value, bool) {
	if target.Kind() == KindBool {
		return v, true
	}
	return Value{}, false
}
