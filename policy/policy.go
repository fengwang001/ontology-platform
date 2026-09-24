// Package policy 维护「角色 -> 可见列集合」的授权映射。
// 状态仅存于进程内存，所有访问只读，可并发使用。
package policy

import (
	"errors"
	"sort"
)

var (
	// ErrUnknownRole 表示策略中不存在该角色。
	ErrUnknownRole = errors.New("policy: unknown role")
	// ErrEmptyRole 表示注册了角色但其可见列集合为空。
	ErrEmptyRole = errors.New("policy: role has empty visible set")
)

// Policy 是角色到可见列集合的不可变映射。
type Policy struct {
	roles map[string]map[string]struct{}
}

// New 根据 rolesToColumns 构造策略；重复列名自动去重。
// 空集合角色被保留（空可见集是合法边界），由 Visible 决定是否报错。
func New(rolesToColumns map[string][]string) *Policy {
	p := &Policy{roles: make(map[string]map[string]struct{}, len(rolesToColumns))}
	for role, cols := range rolesToColumns {
		set := make(map[string]struct{}, len(cols))
		for _, col := range cols {
			set[col] = struct{}{}
		}
		p.roles[role] = set
	}
	return p
}

// Roles 返回全部角色名（字典序）。
func (p *Policy) Roles() []string {
	out := make([]string, 0, len(p.roles))
	for role := range p.roles {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

// Visible 返回角色可见列的字典序列表。
// 角色不存在返回 ErrUnknownRole；可见集为空返回 ErrEmptyRole。
func (p *Policy) Visible(role string) ([]string, error) {
	set, ok := p.roles[role]
	if !ok {
		return nil, ErrUnknownRole
	}
	if len(set) == 0 {
		return nil, ErrEmptyRole
	}
	out := make([]string, 0, len(set))
	for col := range set {
		out = append(out, col)
	}
	sort.Strings(out)
	return out, nil
}

// CanSee 判断某角色能否看见某列；未知角色一律不可见。
func (p *Policy) CanSee(role, column string) bool {
	set, ok := p.roles[role]
	if !ok {
		return false
	}
	_, ok = set[column]
	return ok
}

// Exists 报告角色是否在策略中注册（空可见集也算存在）。
func (p *Policy) Exists(role string) bool {
	_, ok := p.roles[role]
	return ok
}

// VisibleSet 返回角色可见列的集合拷贝（不暴露内部状态）。
func (p *Policy) VisibleSet(role string) (map[string]struct{}, error) {
	cols, err := p.Visible(role)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(cols))
	for _, col := range cols {
		set[col] = struct{}{}
	}
	return set, nil
}
