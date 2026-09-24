// Package policy 维护「角色 -> 可见列集合」的映射，状态存于进程内存。
package policy

import (
	"errors"
	"sort"
)

// ErrUnknownRole 表示查询的角色没有任何策略条目。
var ErrUnknownRole = errors.New("policy: unknown role")

// Policy 是角色到可见列集合的映射。零值不可用，请用 New 构造。
type Policy struct {
	roles map[string]map[string]struct{}
}

// New 返回一个空策略。
func New() *Policy {
	return &Policy{roles: make(map[string]map[string]struct{})}
}

// Grant 把若干列授予角色（幂等，可重复调用累加）。
func (p *Policy) Grant(role string, cols ...string) *Policy {
	set, ok := p.roles[role]
	if !ok {
		set = make(map[string]struct{}, len(cols))
		p.roles[role] = set
	}
	for _, c := range cols {
		set[c] = struct{}{}
	}
	return p
}

// Has 报告角色是否存在策略条目（空可见集也是合法条目）。
func (p *Policy) Has(role string) bool {
	_, ok := p.roles[role]
	return ok
}

// Visible 报告列 col 对角色是否可见；未知角色一律不可见。
func (p *Policy) Visible(role, col string) bool {
	set, ok := p.roles[role]
	if !ok {
		return false
	}
	_, ok = set[col]
	return ok
}

// Columns 返回角色的可见列集合（字典序）；未知角色返回 ErrUnknownRole。
func (p *Policy) Columns(role string) ([]string, error) {
	set, ok := p.roles[role]
	if !ok {
		return nil, ErrUnknownRole
	}
	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols, nil
}
