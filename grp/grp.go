// Package grp 把可空分组键 *string 归一化为可比较的内部键。
// 三种形态：NULL（nil）、空串（&""）、普通串，互不混淆。
package grp

// Kind 分组键形态。
type Kind uint8

const (
	Null  Kind = iota // nil 指针，唯一的 NULL 组
	Empty             // 指向空串 ""，与 NULL 不同组
	Str               // 普通非空串
)

// Key 归一化后的分组键，可比较，可直接做 map 键。
type Key struct {
	Kind Kind
	Val  string // 仅 Str 形态非空
}

// Of 把 *string 归一化为 Key：身份按字符串值，nil 归 NULL 组。
func Of(p *string) Key {
	if p == nil {
		return Key{Kind: Null}
	}
	if *p == "" {
		return Key{Kind: Empty}
	}
	return Key{Kind: Str, Val: *p}
}

// Less 定义枚举顺序：NULL 组、空串组、其余按字符串字典序。
func Less(a, b Key) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind // Null < Empty < Str
	}
	return a.Val < b.Val
}
