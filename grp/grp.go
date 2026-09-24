// Package grp 把可空分组键 *string 归一化为可比较的内部键。
// 三种形态：NULL（nil）、空串（&""）、普通串，互不相同。
package grp

// Key 是归一化后的分组键，可直接作为 map 键。
// 身份按字符串值确定：指向同一字符串值的不同指针归一为同一 Key。
type Key struct {
	null bool   // true 表示 NULL 组（来自 nil）
	s    string // 非 NULL 时的字符串值（可能是 ""）
}

// Normalize 把可空指针归一为 Key。nil → NULL 组；非 nil → 按字符串值。
func Normalize(p *string) Key {
	if p == nil {
		return Key{null: true}
	}
	return Key{s: *p}
}

// IsNull 报告该键是否为 NULL 组。
func (k Key) IsNull() bool { return k.null }

// Str 返回非 NULL 键的字符串值；NULL 键返回 ""。
func (k Key) Str() string { return k.s }

// Less 定义枚举顺序：NULL 组最前，其余按字符串字典序（空串自然最前）。
func (k Key) Less(o Key) bool {
	if k.null != o.null {
		return k.null
	}
	return k.s < o.s
}
