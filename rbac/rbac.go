// Package rbac 实现带层级角色与静态/动态职责分离（SSD/DSD）约束的 RBAC 管理器。
//
// 继承边 (senior, junior) 表示持有 senior 即同时拥有 junior 的全部权限，边构成 DAG。
// Juniors(r) 为 r 本身加上沿边可达的全部 junior。
// Auth(u) 为 u 被分配角色各自的 Juniors 之并；Eff(s) 为会话显式激活角色集 A(s)
// 中各角色的 Juniors 之并。SSD 约束要求任意用户的 Auth 与 RS 之交的元素个数小于 n；
// DSD 约束要求任意会话的 Eff 与 RS 之交的元素个数小于 n。
//
// 所有操作可并发调用，结果等价于某个串行顺序；约束检查与其后的修改是一个原子步骤。
package rbac

import (
	"sort"
	"sync"
)

// Deactivation 表示一次级联停用记录（会话, 角色）。
type Deactivation struct {
	Session string
	Role    string
}

type session struct {
	user   string
	active map[string]bool // 显式激活角色集 A(s)
}

type constraint struct {
	roles map[string]bool
	n     int
}

// Manager 维护角色、继承边、用户、会话、权限与 SSD/DSD 约束。
type Manager struct {
	mu     sync.Mutex
	smax   int
	cmax   int
	roles  map[string]map[string]bool // role -> 权限集
	users  map[string]map[string]bool // user -> 直接分配的角色集
	userS  map[string]map[string]bool // user -> 会话 id 集
	sess   map[string]*session        // sid -> 会话
	edges  map[string]map[string]bool // senior -> 直接 junior 集
	ssd    map[string]*constraint
	dsd    map[string]*constraint
	// 反查索引：角色 -> Auth 含该角色的用户 / Eff 含该角色的会话。
	userIdx map[string]map[string]bool
	sessIdx map[string]map[string]bool
	// 非导出计数器：最近一次 AddInherit/DeleteInherit 检查或重算的用户数与会话数。
	countUsers    int
	countSessions int
}

// NewManager 构造管理器。smax 为每用户会话数上限（1..1000），
// cmax 为约束总数上限（SSD 与 DSD 合计，1..1000），否则整体拒绝。
func NewManager(smax, cmax int) (*Manager, error) {
	if smax < 1 || smax > 1000 || cmax < 1 || cmax > 1000 {
		return nil, errInvalid("invalid-config",
			"invalid limits: smax=%d cmax=%d (both must be in [1,1000])", smax, cmax)
	}
	return &Manager{
		smax:    smax,
		cmax:    cmax,
		roles:   map[string]map[string]bool{},
		users:   map[string]map[string]bool{},
		userS:   map[string]map[string]bool{},
		sess:    map[string]*session{},
		edges:   map[string]map[string]bool{},
		ssd:     map[string]*constraint{},
		dsd:     map[string]*constraint{},
		userIdx: map[string]map[string]bool{},
		sessIdx: map[string]map[string]bool{},
	}, nil
}

// juniors 返回 r 本身加上沿继承边可达的全部 junior。
func (m *Manager) juniors(role string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{role}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[x] {
			continue
		}
		seen[x] = true
		for j := range m.edges[x] {
			stack = append(stack, j)
		}
	}
	return seen
}

// reaches 报告 to 是否属于 Juniors(from)。
func (m *Manager) reaches(from, to string) bool {
	return m.juniors(from)[to]
}

// authOf 计算用户 u 的授权角色集 Auth(u)。
func (m *Manager) authOf(user string) map[string]bool {
	out := map[string]bool{}
	for r := range m.users[user] {
		for x := range m.juniors(r) {
			out[x] = true
		}
	}
	return out
}

// effOf 计算会话 sid 的有效激活集 Eff(sid)。
func (m *Manager) effOf(sid string) map[string]bool {
	out := map[string]bool{}
	for r := range m.sess[sid].active {
		for x := range m.juniors(r) {
			out[x] = true
		}
	}
	return out
}

// violated 返回被集合 set 违反的约束名（|set ∩ RS| >= n），升序。
func violated(set map[string]bool, cons map[string]*constraint) []string {
	var out []string
	for name, c := range cons {
		cnt := 0
		for r := range c.roles {
			if set[r] {
				cnt++
			}
		}
		if cnt >= c.n {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func indexAdd(idx map[string]map[string]bool, key, val string) {
	s := idx[key]
	if s == nil {
		s = map[string]bool{}
		idx[key] = s
	}
	s[val] = true
}

func indexDel(idx map[string]map[string]bool, key, val string) {
	if s := idx[key]; s != nil {
		delete(s, val)
		if len(s) == 0 {
			delete(idx, key)
		}
	}
}

// resyncUser 在用户 Auth 变化后修复反查索引。
func (m *Manager) resyncUser(user string, oldAuth, newAuth map[string]bool) {
	for r := range oldAuth {
		if !newAuth[r] {
			indexDel(m.userIdx, r, user)
		}
	}
	for r := range newAuth {
		if !oldAuth[r] {
			indexAdd(m.userIdx, r, user)
		}
	}
}

// resyncSession 在会话 Eff 变化后修复反查索引；oldEff 为变化前的 Eff。
func (m *Manager) resyncSession(sid string, oldEff map[string]bool) {
	newEff := m.effOf(sid)
	for r := range oldEff {
		if !newEff[r] {
			indexDel(m.sessIdx, r, sid)
		}
	}
	for r := range newEff {
		if !oldEff[r] {
			indexAdd(m.sessIdx, r, sid)
		}
	}
}

// validateConstraint 校验约束的公共参数（名称、RS、n）。
func validateConstraint(name string, roles []string, n int) *Error {
	if name == "" {
		return errInvalid("empty-name", "constraint name must be non-empty")
	}
	for _, r := range roles {
		if r == "" {
			return errInvalid("empty-name", "constraint role names must be non-empty")
		}
	}
	if len(roles) < 2 {
		return errInvalid("invalid-rs", "constraint %q: RS must contain at least 2 roles", name)
	}
	seen := map[string]bool{}
	for _, r := range roles {
		if seen[r] {
			return errInvalid("invalid-rs", "constraint %q: duplicate role %q in RS", name, r)
		}
		seen[r] = true
	}
	if n < 2 || n > len(roles) {
		return errInvalid("invalid-n", "constraint %q: n=%d out of [2,%d]", name, n, len(roles))
	}
	return nil
}

func sortDeactivations(list []Deactivation) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Session != list[j].Session {
			return list[i].Session < list[j].Session
		}
		return list[i].Role < list[j].Role
	})
}
