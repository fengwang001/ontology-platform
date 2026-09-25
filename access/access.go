// Package access 在 acl.Policy 之上做权限求值。
//
// 有效授权 = 主体直接授权 ∪ 其传递闭包内所有祖先组的授权（并集）；
// 直接授权优先短路判定。默认拒绝：并集为空即不允许。
// 组闭包按主体记忆化：同一主体重复求值不再访问组，
// 组访问计数不随求值次数增长。
package access

import (
	"sort"

	"ontology/acl"
)

// Evaluator 对一组主体做权限判定，内部缓存组闭包展开结果。
type Evaluator struct {
	pol *acl.Policy

	ancestors   map[acl.Subject][]acl.Subject // 主体 -> 祖先组闭包（记忆化）
	groupVisits int                           // 组展开访问计数（非导出，供不变量验证）
}

// NewEvaluator 创建基于 pol 的求值器。组成员关系变化后须调用 Reset。
func NewEvaluator(pol *acl.Policy) *Evaluator {
	return &Evaluator{pol: pol, ancestors: make(map[acl.Subject][]acl.Subject)}
}

// Reset 清空闭包缓存与计数器（组成员关系变更后调用）。
func (e *Evaluator) Reset() {
	e.ancestors = make(map[acl.Subject][]acl.Subject)
	e.groupVisits = 0
}

// GroupVisits 返回累计组展开访问次数（用于证明展开不随求值次数线性增长）。
func (e *Evaluator) GroupVisits() int { return e.groupVisits }

// closure 返回 subj 的祖先组传递闭包（含嵌套），带记忆化与环保护。
func (e *Evaluator) closure(subj acl.Subject) []acl.Subject {
	if cached, ok := e.ancestors[subj]; ok {
		return cached
	}
	visited := map[acl.Subject]bool{subj: true}
	queue := []acl.Subject{subj}
	var out []acl.Subject
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, g := range e.pol.ParentsOf(cur) {
			if visited[g] {
				continue
			}
			visited[g] = true
			e.groupVisits++
			out = append(out, g)
			queue = append(queue, g)
		}
	}
	e.ancestors[subj] = out
	return out
}

// Allowed 判定 subj 是否可对 prop 执行动作 a。
// 直接授权优先短路；否则查祖先组闭包授权的并集；默认拒绝。
func (e *Evaluator) Allowed(subj acl.Subject, prop string, a acl.Action) bool {
	if e.pol.Direct(subj, prop, a) {
		return true
	}
	for _, g := range e.closure(subj) {
		if e.pol.Direct(g, prop, a) {
			return true
		}
	}
	return false
}

// Evaluate 批量判定 subj 对 props 中每个属性执行动作 a 是否被允许。
func (e *Evaluator) Evaluate(subj acl.Subject, props []string, a acl.Action) map[string]bool {
	out := make(map[string]bool, len(props))
	for _, p := range props {
		out[p] = e.Allowed(subj, p, a)
	}
	return out
}

// Denied 返回 props 中 subj 对动作 a 无权限的属性名（升序，便于错误信息稳定）。
func (e *Evaluator) Denied(subj acl.Subject, props []string, a acl.Action) []string {
	var out []string
	for _, p := range props {
		if !e.Allowed(subj, p, a) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
