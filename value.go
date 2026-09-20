package ontology

// Value 是一个属性值：要么是字符串，要么是 NULL。
// 空字符串与 NULL 是两种不同的值，绝不混用。
type Value struct {
	s    string
	null bool
}

// String 构造一个字符串值（允许为空字符串）。
func String(s string) Value { return Value{s: s} }

// Null 构造一个 NULL 值。
func Null() Value { return Value{null: true} }

// IsNull 报告该值是否为 NULL。
func (v Value) IsNull() bool { return v.null }

// Raw 返回调用方提供的原始字符串（对 NULL 返回空串）。
// 返回值绝不经过规范化，保持原始大小写、空白与码点序列。
func (v Value) Raw() string { return v.s }

// String 实现 fmt.Stringer，仅用于展示。
func (v Value) String() string {
	if v.null {
		return "NULL"
	}
	return v.s
}
