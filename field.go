package ontology

// Kind 是字段值的类型。
type Kind int

const (
	// KindString 字符串字段。
	KindString Kind = iota + 1
	// KindInt 整数字段，带上下界。
	KindInt
)

// FieldDecl 描述一个已声明字段的类型与（整数的）取值范围。
// Min/Max 仅对 KindInt 生效。
type FieldDecl struct {
	Name string
	Kind Kind
	Min  int64
	Max  int64
}

// Value 是配置字段的运行时值，要么是字符串要么是整数。
type Value struct {
	str  string
	num  int64
	kind Kind
}

// String 构造字符串值。
func String(v string) Value { return Value{str: v, kind: KindString} }

// Int 构造整数值。
func Int(v int64) Value { return Value{num: v, kind: KindInt} }

// IsString 报告值是否为字符串类型。
func (v Value) IsString() bool { return v.kind == KindString }

// IsInt 报告值是否为整数类型。
func (v Value) IsInt() bool { return v.kind == KindInt }

// AsString 返回字符串值。
func (v Value) AsString() string { return v.str }

// AsInt 返回整数值。
func (v Value) AsInt() int64 { return v.num }

// Fields 是一次配置内容里“字段名 -> 值”的映射。
type Fields map[string]Value
