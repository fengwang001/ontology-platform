// Package constexpr 实现静态语言的结构化常量表达式求值器。
//
// 它对调用方手工构造的表达式树求值，区分无类型常量（带“种类”：
// 整数/有理数/布尔/字符串，任意精度）与有类型常量（带具体类型，
// 逐步做可表示性检查），并提供并发安全的命名常量表。
package constexpr

// Kind 是无类型常量（以及值本身）的种类。
type Kind uint8

const (
	KindInt Kind = iota + 1
	KindRat
	KindBool
	KindString
)

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "<invalid>"
}

var kindNames = [...]string{
	0:          "<invalid>",
	KindInt:    "int",
	KindRat:    "rat",
	KindBool:   "bool",
	KindString: "string",
}
