// Package project 做读时投影：按调用主体的读权限移除对象中不可见的属性。
//
// 不可见属性被**移除**（键消失），而非置零值或掩码——零值与真实缺失必须
// 可区分。投影结果是读取视图，允许不满足 schema（缺必填列），不影响存储
// 中对象的完整性（见 DESIGN.md 推导点 1）。
package project

import (
	"sort"

	"ontology/access"
	"ontology/acl"
)

// Object 是一个查询结果对象：属性名 -> 值。
type Object map[string]any

// Projector 按主体读权限投影对象。
type Projector struct {
	ev *access.Evaluator

	examined int // 非导出计数器：投影过程中考察的属性总数（证明确定性）
}

// NewProjector 创建基于求值器的投影器。
func NewProjector(ev *access.Evaluator) *Projector {
	return &Projector{ev: ev}
}

// Project 返回 obj 中 subject 可见属性的投影。
//
// 为保证确定性（同一主体、同一对象、任意遍历顺序结果逐字节相同），
// 属性按名称排序后逐个判定；原对象不被修改。
func (p *Projector) Project(obj Object, subject acl.Subject) Object {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(Object, len(keys))
	for _, k := range keys {
		p.examined++
		if p.ev.Allowed(subject, k, acl.Read) {
			out[k] = obj[k]
		}
	}
	return out
}

// ProjectAll 对一组对象逐个投影。
func (p *Projector) ProjectAll(objs []Object, subject acl.Subject) []Object {
	out := make([]Object, len(objs))
	for i, obj := range objs {
		out[i] = p.Project(obj, subject)
	}
	return out
}
