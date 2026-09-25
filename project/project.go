// Package project 读时投影：按调用主体的读权限裁剪查询结果对象。
//
// 不可见属性被整体移除（键缺席），不是置零值、不是掩码——
// 因此"真实缺失"与"零值"在投影结果中可区分。
// 投影按键排序构造新 map，同主体同对象任意遍历顺序结果逐字节相同。
// 投影只产生读取视图，允许缺 schema 必填列，永不回写存储。
package project

import "sort"

// Object 是一个查询结果对象：属性名 -> 值。
type Object map[string]any

// VisibleFunc 判定某属性对当前调用主体是否可读。
type VisibleFunc func(attr string) bool

// Projector 执行投影并统计被移除的属性数。
type Projector struct {
	removed int // 非导出计数器：累计被移除的属性数
}

// NewProjector 创建投影器。
func NewProjector() *Projector { return &Projector{} }

// Project 返回 obj 的读投影：仅保留 visible 判定为真的键。
// 不修改入参；返回新对象。
func (p *Projector) Project(obj Object, visible VisibleFunc) Object {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(Object, len(keys))
	for _, k := range keys {
		if visible(k) {
			out[k] = obj[k]
		} else {
			p.removed++
		}
	}
	return out
}

// Removed 返回累计被移除的属性数（非导出计数器的只读视图）。
func (p *Projector) Removed() int { return p.removed }
