// Package authz 实现层级资源授权决策缓存。
//
// 缓存以（主体，动作，资源路径）为键保存允许/拒绝结果及其依据节点；
// 规则变更时只失效（依据节点，结果）二元组真正发生变化的条目。
package authz

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// BasisNone 表示依据节点为「无」（没有任何匹配规则，默认拒绝）。
const BasisNone = ""

// ErrCode 是被拒绝操作的原因码，各类原因可互相区分。
type ErrCode int

const (
	// ErrInvalidPath 路径不以斜杠开头，或含空段（根 "/" 除外）。
	ErrInvalidPath ErrCode = iota + 1
	// ErrEmptySubject 主体为空。
	ErrEmptySubject
	// ErrEmptyAction 动作为空。
	ErrEmptyAction
	// ErrRuleLimit 规则数已达上限时设置新规则。
	ErrRuleLimit
	// ErrRuleNotFound 删除不存在的规则。
	ErrRuleNotFound
)

// Error 描述一次被整体拒绝的操作，Code 可用于区分原因。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Decision 是一次授权判定结果，连同依据节点一并返回。
type Decision struct {
	Allow bool
	// Basis 为依据节点路径；BasisNone 表示「无」。
	Basis string
}

// Stats 是对外暴露的三项统计。
type Stats struct {
	Hits        uint64 // 命中数
	Computes    uint64 // 现算数
	Invalidated uint64 // 被失效条数（仅由规则变更与已缓存条目决定）
}

// CachedEntry 是缓存条目的只读快照，用于检查与调试。
type CachedEntry struct {
	Subject  string
	Action   string
	Path     string
	Decision Decision
}

type saKey struct {
	subject string
	action  string
}

// rulesSnap 是不可变的规则快照；写操作整体换入新快照，
// 读操作（现算）无需加锁即可拿到一致视图。
type rulesSnap struct {
	byNode map[string]map[saKey]bool // 节点路径 -> (主体,动作) -> 允许/拒绝
	count  int
}

type entryKey struct {
	subject string
	action  string
	path    string
}

type entry struct {
	allow bool
	basis string
}

// Cache 是层级资源授权决策缓存，可并发使用。
type Cache struct {
	maxRules int

	wmu   sync.Mutex // 串行化规则写操作（设置/删除）
	rules atomic.Pointer[rulesSnap]

	mu          sync.Mutex // 保护 entries 与统计
	entries     map[entryKey]entry
	hits        uint64
	computes    uint64
	invalidated uint64

	// testHookBeforeWrite 在现算完成后、回填缓存前调用，仅测试使用。
	testHookBeforeWrite func()
}

// New 创建缓存。maxRules 为规则总数上限，<= 0 表示不限。
func New(maxRules int) *Cache {
	c := &Cache{
		maxRules: maxRules,
		entries:  make(map[entryKey]entry),
	}
	c.rules.Store(&rulesSnap{byNode: make(map[string]map[saKey]bool)})
	return c
}

// SetRule 在节点 path 上设置（主体，动作）的规则；同节点同主体同动作再设即覆盖。
// 覆盖为相同取值时不改变规则快照、不失效任何条目。
func (c *Cache) SetRule(path, subject, action string, allow bool) error {
	if err := validatePath(path); err != nil {
		return err
	}
	if err := validateSubjectAction(subject, action); err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()

	old := c.rules.Load()
	key := saKey{subject, action}
	oldNode := old.byNode[path]
	oldAllow, exists := oldNode[key]
	if exists && oldAllow == allow {
		return nil // 覆盖为相同取值：规则不变，零失效
	}
	if !exists && c.maxRules > 0 && old.count >= c.maxRules {
		return newError(ErrRuleLimit, "规则数已达上限 %d，无法设置新规则", c.maxRules)
	}

	next := &rulesSnap{byNode: make(map[string]map[saKey]bool, len(old.byNode)+1)}
	for p, m := range old.byNode {
		if p != path {
			next.byNode[p] = m
		}
	}
	nm := make(map[saKey]bool, len(oldNode)+1)
	for k, v := range oldNode {
		nm[k] = v
	}
	nm[key] = allow
	next.byNode[path] = nm
	next.count = old.count
	if !exists {
		next.count++
	}
	c.rules.Store(next)

	c.invalidate(next, key)
	return nil
}

// DeleteRule 删除节点 path 上（主体，动作）的规则。
func (c *Cache) DeleteRule(path, subject, action string) error {
	if err := validatePath(path); err != nil {
		return err
	}
	if err := validateSubjectAction(subject, action); err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()

	old := c.rules.Load()
	key := saKey{subject, action}
	oldNode, nodeExists := old.byNode[path]
	if _, exists := oldNode[key]; !nodeExists || !exists {
		return newError(ErrRuleNotFound, "规则不存在: 节点=%q 主体=%q 动作=%q", path, subject, action)
	}

	next := &rulesSnap{byNode: make(map[string]map[saKey]bool, len(old.byNode)), count: old.count - 1}
	for p, m := range old.byNode {
		if p != path {
			next.byNode[p] = m
		}
	}
	if len(oldNode) > 1 {
		nm := make(map[saKey]bool, len(oldNode)-1)
		for k, v := range oldNode {
			if k != key {
				nm[k] = v
			}
		}
		next.byNode[path] = nm
	}
	c.rules.Store(next)

	c.invalidate(next, key)
	return nil
}

// Decide 返回（主体，动作，资源路径）的判定结果；命中缓存则直接返回，
// 否则现算并回填。可被并发调用。
func (c *Cache) Decide(subject, action, path string) (Decision, error) {
	if err := validatePath(path); err != nil {
		return Decision{}, err
	}
	if err := validateSubjectAction(subject, action); err != nil {
		return Decision{}, err
	}
	ek := entryKey{subject: subject, action: action, path: path}
	for {
		snap := c.rules.Load()

		c.mu.Lock()
		e, ok := c.entries[ek]
		c.mu.Unlock()
		if ok {
			c.mu.Lock()
			c.hits++
			c.mu.Unlock()
			return Decision{Allow: e.allow, Basis: e.basis}, nil
		}

		allow, basis := compute(snap, subject, action, path)

		if c.testHookBeforeWrite != nil {
			c.testHookBeforeWrite()
		}

		// 回填前校验规则快照未变；若已变则丢弃旧结果重试，
		// 保证不会把已被变更影响的旧结果写入缓存。
		c.mu.Lock()
		c.computes++
		if c.rules.Load() == snap {
			if e, ok := c.entries[ek]; ok {
				c.mu.Unlock()
				return Decision{Allow: e.allow, Basis: e.basis}, nil
			}
			c.entries[ek] = entry{allow: allow, basis: basis}
			c.mu.Unlock()
			return Decision{Allow: allow, Basis: basis}, nil
		}
		c.mu.Unlock()
	}
}

// invalidate 在新规则快照下复核同主体同动作的已缓存条目，
// 仅删除（依据节点，结果）二元组发生变化的条目。
func (c *Cache) invalidate(snap *rulesSnap, key saKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ek, e := range c.entries {
		if ek.subject != key.subject || ek.action != key.action {
			continue
		}
		allow, basis := compute(snap, key.subject, key.action, ek.path)
		if allow != e.allow || basis != e.basis {
			delete(c.entries, ek)
			c.invalidated++
		}
	}
}

// Recompute 不经过缓存直接现算，用于校验缓存一致性与调试。
func (c *Cache) Recompute(subject, action, path string) Decision {
	allow, basis := compute(c.rules.Load(), subject, action, path)
	return Decision{Allow: allow, Basis: basis}
}

// Stats 返回命中数、现算数、被失效条数。
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{Hits: c.hits, Computes: c.computes, Invalidated: c.invalidated}
}

// RuleCount 返回当前规则总数。
func (c *Cache) RuleCount() int {
	return c.rules.Load().count
}

// CachedEntries 返回当前缓存条目的快照，用于测试与调试。
func (c *Cache) CachedEntries() []CachedEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CachedEntry, 0, len(c.entries))
	for k, e := range c.entries {
		out = append(out, CachedEntry{
			Subject:  k.subject,
			Action:   k.action,
			Path:     k.path,
			Decision: Decision{Allow: e.allow, Basis: e.basis},
		})
	}
	return out
}

// compute 在规则快照上现算：从资源自身起沿祖先向上找最近规则。
func compute(snap *rulesSnap, subject, action, path string) (allow bool, basis string) {
	key := saKey{subject, action}
	for p := path; ; p = parentPath(p) {
		if m, ok := snap.byNode[p]; ok {
			if a, ok := m[key]; ok {
				return a, p
			}
		}
		if p == "/" {
			return false, BasisNone
		}
	}
}

// parentPath 返回路径的父节点；根节点的父节点是自身。
func parentPath(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

// validatePath 校验资源/节点路径：根为 "/"，其余必须以 "/" 开头且不含空段。
func validatePath(path string) error {
	if path == "/" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return newError(ErrInvalidPath, "路径必须以斜杠开头: %q", path)
	}
	if strings.HasSuffix(path, "/") || strings.Contains(path, "//") {
		return newError(ErrInvalidPath, "路径含空段: %q", path)
	}
	return nil
}

// validateSubjectAction 校验主体与动作非空，主体先于动作。
func validateSubjectAction(subject, action string) error {
	if subject == "" {
		return newError(ErrEmptySubject, "主体为空")
	}
	if action == "" {
		return newError(ErrEmptyAction, "动作为空")
	}
	return nil
}
