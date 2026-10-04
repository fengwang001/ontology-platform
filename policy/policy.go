// Package policy 维护各分组策略并对设备逐键求有效配置。
package policy

import (
	"sort"

	"ontology/group"
)

const (
	// MaxEntries 是单个分组策略的键数上限。
	MaxEntries = 32
)

// Value 是一个策略值：普通字符串（含空串）或置空标记 Unset。
type Value struct {
	set bool
	val string
}

// String 构造普通值，空串也是普通值。
func String(v string) Value { return Value{set: true, val: v} }

// Unset 构造置空标记：遮蔽更低优先级的同名键。
func Unset() Value { return Value{} }

// IsSet 报告是否为普通值（false 表示 Unset）。
func (v Value) IsSet() bool { return v.set }

// String 返回普通值内容；Unset 时返回 ("", false)。
func (v Value) Get() (string, bool) { return v.val, v.set }

// Policy 是一组键到值的整份策略映射。
type Policy map[string]Value

// GroupView 是求值器所需的 group 只读接口。
type GroupView interface {
	GroupsOf(dev string) []string
	HasGroup(name string) bool
	Priority(name string) (int, bool)
}

// Store 保存每个分组的整份策略。
type Store struct {
	policies map[string]Policy
}

// New 创建策略存储，内置分组 "*" 初始为空策略。
func New() *Store {
	return &Store{policies: map[string]Policy{group.Star: {}}}
}

// Clone 返回深拷贝。
func (st *Store) Clone() *Store {
	c := &Store{policies: make(map[string]Policy, len(st.policies))}
	for g, p := range st.policies {
		cp := make(Policy, len(p))
		for k, v := range p {
			cp[k] = v
		}
		c.policies[g] = cp
	}
	return c
}

// AddGroup 为空策略的新分组登记策略槽。
func (st *Store) AddGroup(name string) {
	if _, ok := st.policies[name]; !ok {
		st.policies[name] = Policy{}
	}
}

// RemoveGroup 删除分组策略；分组须已无成员（由调用方保证）。
func (st *Store) RemoveGroup(name string) { delete(st.policies, name) }

// Get 返回某分组策略的拷贝；不存在时 ok=false。
func (st *Store) Get(name string) (Policy, bool) {
	p, ok := st.policies[name]
	if !ok {
		return nil, false
	}
	out := make(Policy, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out, true
}

// Validate 校验策略：键为 1..64 字节非空字节串，键数 0..32。
func Validate(p map[string]Value) error {
	if len(p) > MaxEntries {
		return group.ErrInvalid
	}
	for k := range p {
		if len(k) < 1 || len(k) > 64 {
			return group.ErrInvalid
		}
	}
	return nil
}

// Set 整份替换某分组策略。
func (st *Store) Set(name string, p map[string]Value) error {
	if name != group.Star && !group.ValidName(name) {
		return group.ErrInvalid
	}
	if err := Validate(p); err != nil {
		return err
	}
	if _, ok := st.policies[name]; !ok {
		return group.ErrNotFound
	}
	np := make(Policy, len(p))
	for k, v := range p {
		np[k] = v
	}
	st.policies[name] = np
	return nil
}

// keyWinner 在设备所属分组中选出某键的胜出名目：
// pr 最大者；pr 并列取名字字节序最小者。不存在含该键的分组时 ok=false。
func keyWinner(gv GroupView, st *Store, dev, key string) (Value, bool) {
	var winner Value
	winnerPr := 0
	winnerName := ""
	found := false
	for _, g := range gv.GroupsOf(dev) {
		p, ok := st.policies[g]
		if !ok {
			continue
		}
		v, ok := p[key]
		if !ok {
			continue
		}
		pr, _ := gv.Priority(g)
		if !found || pr > winnerPr || pr == winnerPr && g < winnerName {
			found = true
			winner = v
			winnerPr = pr
			winnerName = g
		}
	}
	return winner, found
}

// Effective 计算设备有效配置。胜者为 Unset 的键不出现在结果中
// （遮蔽更低优先级，不回落）。结果不为 nil。
func Effective(gv GroupView, st *Store, dev string) map[string]string {
	out := map[string]string{}
	keys := map[string]struct{}{}
	for _, g := range gv.GroupsOf(dev) {
		for k := range st.policies[g] {
			keys[k] = struct{}{}
		}
	}
	for k := range keys {
		w, ok := keyWinner(gv, st, dev, k)
		if ok && w.set {
			out[k] = w.val
		}
	}
	return out
}

// Keys 返回策略中出现过的全部键（按字节序），供测试与诊断使用。
func (st *Store) Keys() []string {
	seen := map[string]struct{}{}
	for _, p := range st.policies {
		for k := range p {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
