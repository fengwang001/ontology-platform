// Package policy 维护角色到可见列集合的映射（进程内存，仅标准库）。
package policy

import "sort"

// Policy 是不可变的列级授权视图：列全集按字典序固定，每个角色映射到可见列集合。
type Policy struct {
	columns []string
	visible map[string]map[string]struct{}
}

// New 根据列全集与“角色 -> 可见列”构造策略。
// 未知列会被忽略；重复列名去重；全集最终按字典序排列。
func New(columns []string, grants map[string][]string) *Policy {
	uniq := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		uniq[col] = struct{}{}
	}
	all := make([]string, 0, len(uniq))
	for col := range uniq {
		all = append(all, col)
	}
	sort.Strings(all)

	index := make(map[string]int, len(all))
	for i, col := range all {
		index[col] = i
	}

	visible := make(map[string]map[string]struct{}, len(grants))
	for role, cols := range grants {
		set := make(map[string]struct{}, len(cols))
		for _, col := range cols {
			if _, ok := index[col]; ok {
				set[col] = struct{}{}
			}
		}
		visible[role] = set
	}
	return &Policy{columns: all, visible: visible}
}

// Columns 返回列全集（字典序，调用方不得修改）。
func (p *Policy) Columns() []string { return p.columns }

// Visible 返回角色可见列的字典序列表；未知角色视为空可见集。
func (p *Policy) Visible(role string) []string {
	set := p.visible[role]
	out := make([]string, 0, len(set))
	for _, col := range p.columns {
		if _, ok := set[col]; ok {
			out = append(out, col)
		}
	}
	return out
}

// Hidden 返回角色不可见列的字典序列表（全集差集）。
func (p *Policy) Hidden(role string) []string {
	set := p.visible[role]
	out := make([]string, 0)
	for _, col := range p.columns {
		if _, ok := set[col]; !ok {
			out = append(out, col)
		}
	}
	return out
}

// IsVisible 报告列对角色是否可见；全集之外的列返回 false。
func (p *Policy) IsVisible(role, col string) bool {
	set := p.visible[role]
	_, ok := set[col]
	return ok
}

// Knows 报告列是否属于策略全集。
func (p *Policy) Knows(col string) bool {
	for _, known := range p.columns {
		if known == col {
			return true
		}
	}
	return false
}
