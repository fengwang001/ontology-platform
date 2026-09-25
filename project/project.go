// Package project 实现读时投影：按调用主体的读权限移除不可见属性。
//
// 不可见属性被**移除**（键不存在），而非置零值或掩码——
// 零值与真实缺失必须可区分。投影按属性名排序遍历，
// 同一主体、同一对象、任意输入遍历顺序，输出内容完全一致。
// 投影产物是读取视图（View），允许缺 schema 必填列（Partial=true），
// 只影响读取呈现，不影响存储完整性。
package project

import (
	"sort"

	"ontology/access"
	"ontology/acl"
)

// Object 是查询结果对象：属性名 -> 值。
type Object map[string]any

// View 是投影后的读取视图。Partial 为 true 表示有必填列因不可见被移除，
// 视图是「部分可见」的降级产物，而非非法对象。
type View struct {
	Object  Object
	Partial bool
	Missing []string // 被投影移除的必填列（升序）
}

// Projector 对固定主体做读投影，内部累计移除计数。
type Projector struct {
	eval    *access.Evaluator
	subj    acl.Subject
	removed int // 非导出计数器：累计被移除的属性数
}

// NewProjector 创建针对 subj 的投影器。
func NewProjector(eval *access.Evaluator, subj acl.Subject) *Projector {
	return &Projector{eval: eval, subj: subj}
}

// Removed 返回累计被移除的属性数（用于证明移除而非置零值）。
func (p *Projector) Removed() int { return p.removed }

// Apply 返回移除不可见属性后的投影对象。输入不被修改。
func (p *Projector) Apply(obj Object) Object {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(Object, len(obj))
	for _, k := range keys {
		if p.eval.Allowed(p.subj, k, acl.Read) {
			out[k] = obj[k]
		} else {
			p.removed++
		}
	}
	return out
}

// ApplyWithSchema 在 Apply 之上标注必填列缺失情况：
// 必填列被移除时视图降级为部分可见（Partial=true），而非判定为非法对象。
func (p *Projector) ApplyWithSchema(obj Object, required []string) View {
	view := p.Apply(obj)
	v := View{Object: view}
	req := append([]string(nil), required...)
	sort.Strings(req)
	for _, r := range req {
		if _, visible := view[r]; !visible {
			if _, existed := obj[r]; existed {
				v.Partial = true
				v.Missing = append(v.Missing, r)
			}
		}
	}
	return v
}
