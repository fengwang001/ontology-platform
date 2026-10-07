package ontology

// ValueType 是字段取值类型。
type ValueType string

const (
	TString ValueType = "string"
	TInt    ValueType = "int"
	TBool   ValueType = "bool"
)

// Value 是一个字段取值。Missing 为 true 时表示"缺失取值"，
// 缺失取值在对应关系中被当作一种独立的取值参与唯一性判断。
type Value struct {
	Type    ValueType
	Missing bool
	Str     string
	Int     int64
	Bool    bool
}

// MissingValue 返回缺失取值。
func MissingValue() Value { return Value{Missing: true} }

// StrValue 构造字符串取值。
func StrValue(s string) Value { return Value{Type: TString, Str: s} }

// IntValue 构造整数取值。
func IntValue(i int64) Value { return Value{Type: TInt, Int: i} }

// BoolValue 构造布尔取值。
func BoolValue(b bool) Value { return Value{Type: TBool, Bool: b} }

// FieldDef 描述一个可作为绑定依据的字段定义。
type FieldDef struct {
	ID       string
	Type     ValueType
	Nullable bool
	// Enum 非空时字段取值空间为该有限集合；为空时取值空间为该类型的无界全域。
	Enum []Value
}

// Domain 是字段的取值空间。
type Domain struct {
	Unbounded bool
	Type      ValueType
	Values    map[Value]bool // Unbounded 为 false 时有效；Nullable 时包含 MissingValue()
}

// Domain 计算字段的取值空间，Nullable 时把缺失取值并入。
func (f FieldDef) Domain() Domain {
	if f.Enum == nil {
		return Domain{Unbounded: true, Type: f.Type}
	}
	values := make(map[Value]bool, len(f.Enum)+1)
	for _, v := range f.Enum {
		values[v] = true
	}
	if f.Nullable {
		values[MissingValue()] = true
	}
	return Domain{Type: f.Type, Values: values}
}

// Contains 判断取值是否落在取值空间内。
func (d Domain) Contains(v Value) bool {
	if v.Missing {
		_, ok := d.Values[v]
		return ok
	}
	if d.Unbounded {
		return v.Type == d.Type
	}
	return d.Values[v]
}
