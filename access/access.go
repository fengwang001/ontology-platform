// Package access 做权限求值：给定主体、属性与动作，判定是否允许。
//
// 规则（见 DESIGN.md）：
//   - 默认拒绝：无显式授权即不允许；
//   - 并集继承：主体所在所有组（含嵌套展开）的允许记录取并集；
//   - 直接优先：主体自身的显式记录（允许或拒绝）覆盖组继承结果。
//
// 组展开带记忆化：同一主体的祖先组只展开一次，访问数不随组总数增长。
package access

import "ontology/acl"

// Evaluator 对 acl.Store 做权限求值，并缓存组展开结果。
type Evaluator struct {
	store *acl.Store

	ancestorCache map[acl.Subject][]acl.Subject
	expansions    int // 非导出计数器：实际发生的组展开次数（缓存未命中才增长）
}

// NewEvaluator 创建基于 store 的求值器。
func NewEvaluator(store *acl.Store) *Evaluator {
	return &Evaluator{
		store:         store,
		ancestorCache: make(map[acl.Subject][]acl.Subject),
	}
}

// ancestors 返回主体的全部祖先组（含嵌套，去重，不含主体自身）。
// 结果按发现顺序排列并缓存；重复调用不重复展开。
func (e *Evaluator) ancestors(s acl.Subject) []acl.Subject {
	if cached, ok := e.ancestorCache[s]; ok {
		return cached
	}
	e.expansions++
	var result []acl.Subject
	visited := map[acl.Subject]bool{s: true}
	queue := []acl.Subject{s}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, parent := range e.store.Parents(cur) {
			if visited[parent] {
				continue
			}
			visited[parent] = true
			result = append(result, parent)
			queue = append(queue, parent)
		}
	}
	e.ancestorCache[s] = result
	return result
}

// Allowed 判定 subject 对 property 的 action 是否被允许。
func (e *Evaluator) Allowed(subject acl.Subject, property string, action acl.Action) bool {
	// 直接授权（含显式拒绝）优先于组继承。
	if allowed, exists := e.store.DirectAllowed(subject, property, action); exists {
		return allowed
	}
	// 组继承：任一祖先组允许即允许（并集）。
	for _, group := range e.ancestors(subject) {
		if allowed, exists := e.store.DirectAllowed(group, property, action); exists && allowed {
			return true
		}
	}
	// 默认拒绝。
	return false
}

// Decision 是单个属性的判定结果。
type Decision struct {
	Property string
	Allowed  bool
}

// Evaluate 批量判定一组属性，返回与输入顺序一致的结果。
func (e *Evaluator) Evaluate(subject acl.Subject, action acl.Action, properties ...string) []Decision {
	decisions := make([]Decision, len(properties))
	for i, p := range properties {
		decisions[i] = Decision{Property: p, Allowed: e.Allowed(subject, p, action)}
	}
	return decisions
}

// Visible 返回 properties 中主体可读（或被指定动作允许）的子集，保持输入顺序。
func (e *Evaluator) Visible(subject acl.Subject, action acl.Action, properties []string) []string {
	var out []string
	for _, p := range properties {
		if e.Allowed(subject, p, action) {
			out = append(out, p)
		}
	}
	return out
}
