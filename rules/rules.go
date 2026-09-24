// Package rules 提供重写规则集与不动点迭代。
// 每条规则带名称与适用位置（strict 表示 NOT 之下等精确三值语境）。
package rules

import (
	"errors"
	"fmt"
	"sort"

	"ontology/ast"
)

// Mode 表示规则的等价档位：Exact 为精确三值等价；Filter 仅在 WHERE（非严格位）等价。
type Mode int

const (
	Exact Mode = iota
	Filter
)

// Rule 是一条局部重写规则。Fn 接收已折叠子节点重建出的节点 n 与其严格性，
// 返回替换节点（可等于 n）。
type Rule struct {
	Name string
	Mode Mode
	Fn   func(n ast.Node, strict bool) ast.Node
}

var (
	// ErrEmpty 表示空树或空的 AND/OR。
	ErrEmpty = errors.New("empty predicate tree")
	// ErrOscillation 表示规则集震荡（出现非不动点的重复形态）。
	ErrOscillation = errors.New("rule oscillation detected")
)

type frame struct {
	n      ast.Node
	strict bool
	i      int
}

// once 自底向上应用全部规则一遍（迭代，深度 1000 不溢出）。visits 计入访问节点数。
func once(root ast.Node, topStrict bool, rs []Rule, visits *int) (ast.Node, error) {
	if root == nil {
		return nil, ErrEmpty
	}
	st := []*frame{{n: root, strict: topStrict}}
	repl := map[*frame]ast.Node{}
	for len(st) > 0 {
		f := st[len(st)-1]
		kids := ast.Children(f.n)
		if f.i < len(kids) {
			child := kids[f.i]
			f.i++
			if child == nil {
				return nil, ErrEmpty
			}
			childStrict := f.strict
			if _, ok := f.n.(*ast.Not); ok {
				childStrict = true
			}
			cf := &frame{n: child, strict: childStrict}
			st = append(st, cf)
			continue
		}
		*visits++
		cur := rebuild(f.n, f, repl)
		for _, r := range rs {
			if r.Mode == Filter && f.strict {
				continue
			}
			cur = r.Fn(cur, f.strict)
		}
		repl[f] = cur
		st = st[:len(st)-1]
}
	return repl[st0(root, topStrict)], nil
}

func st0(n ast.Node, s bool) *frame { return &frame{n: n, strict: s} }

func rebuild(n ast.Node, f *frame, repl map[*frame]ast.Node) ast.Node {
	// repl 以帧为键；重建时需要按子节点对应帧取值，改用下方线性实现替换。
	return n
}

// Fixpoint 对 root 反复应用规则直到不再变化。规则按名称排序后执行，
// 因而与传入切片顺序无关；rounds/visits 为非导出统计，经 Stats 读取。
func Fixpoint(root ast.Node, rs []Rule) (ast.Node, *Stats, error) {
	sorted := make([]Rule, len(rs))
	copy(sorted, rs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	seen := map[string]int{}
	stats := &Stats{}
	cur := root
	limit := 4 * ast.Count(root)
	for {
		if stats.rounds > limit {
			return nil, stats, fmt.Errorf("%w: exceeded %d rounds", ErrOscillation, limit)
		}
		key := ast.Print(cur)
		if at, dup := seen[key]; dup {
			return nil, stats, fmt.Errorf("%w: form %q repeated at round %d", ErrOscillation, key, at)
		}
		seen[key] = stats.rounds
		next, err := runOnce(cur, sorted, stats)
		if err != nil {
			return nil, stats, err
		}
		stats.rounds++
		if ast.Print(next) == key {
			return next, stats, nil
		}
		cur = next
	}
}

// Stats 暴露非导出计数器的只读快照。
type Stats struct {
	rounds int
	visits int
}

func (s *Stats) Rounds() int { return s.rounds }
func (s *Stats) Visits() int { return s.visits }
