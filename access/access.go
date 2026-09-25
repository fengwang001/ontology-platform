// Package access 做权限求值：给定主体与 (属性, 动作)，判定是否允许。
//
// 判定规则（与 DESIGN.md 一致）：
//  1. 主体自身有显式条目（允许/拒绝）时，直接采用——直接授权覆盖组授权；
//  2. 否则取主体所在全部组（含嵌套祖先）授权的并集：任一组允许即允许；
//  3. 都没有记录时默认拒绝。
//
// 组闭包按主体惰性展开且只展开一次，之后任意多次判定复用，
// 展开访问数不随判定属性数增长。
package access

import "ontology/acl"

// Action 复用 acl.Perm：Read / Write。
type Action = acl.Perm

const (
	Read  = acl.Read
	Write = acl.Write
)

// Checker 针对单一主体的权限判定器。用 NewChecker 构造。
type Checker struct {
	store   *acl.Store
	subject acl.Subject

	groups     []string // 祖先组闭包（含直接组与嵌套祖先），惰性填充
	expanded   bool
	seenMut    int // 上次展开时观察到的存储变更计数
	expansions int // 非导出计数：闭包展开时访问过的节点数
}

// NewChecker 创建 subject 的判定器。
func NewChecker(store *acl.Store, subject acl.Subject) *Checker {
	return &Checker{store: store, subject: subject}
}

// closure 一次性 BFS 展开主体所在的全部祖先组（visited 去重，
// 嵌套组与菱形共享父组都只访问一次）。
// 存储发生变更（授权/成员关系）后自动重新展开，避免陈旧结论。
func (c *Checker) closure() []string {
	if c.expanded && c.seenMut == c.store.Mutations() {
		return c.groups
	}
	c.expanded = true
	c.seenMut = c.store.Mutations()
	c.groups = c.groups[:0]
	visited := map[acl.Subject]bool{c.subject: true}
	queue := []acl.Subject{c.subject}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		c.expansions++
		for _, gid := range c.store.ParentGroups(cur) {
			g := acl.GroupSubject(gid)
			if visited[g] {
				continue
			}
			visited[g] = true
			c.groups = append(c.groups, gid)
			queue = append(queue, g)
		}
	}
	return c.groups
}

// Allowed 判定 subject 对 attr 执行 act 是否被允许。
func (c *Checker) Allowed(attr string, act Action) bool {
	if allow, set := c.store.DirectEntry(c.subject, attr, act); set {
		return allow
	}
	for _, gid := range c.closure() {
		if allow, set := c.store.GroupEntry(gid, attr, act); set && allow {
			return true
		}
	}
	return false
}

// Decide 批量判定一组属性，返回每个属性是否允许。
func (c *Checker) Decide(attrs []string, act Action) map[string]bool {
	out := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		out[a] = c.Allowed(a, act)
	}
	return out
}

// Expansions 返回闭包展开访问过的节点数（非导出计数的只读视图）。
func (c *Checker) Expansions() int { return c.expansions }
