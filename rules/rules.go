// Package rules 提供保语义重写规则接口、不动点引擎与终止/震荡检测。
package rules

import (
	"fmt"

	"ontology/ast"
)

// Rule 为一条自下而上的局部重写规则：对单个节点返回是否改写及新节点。
type Rule interface {
	Name() string
	Apply(n ast.Node) (ast.Node, bool)
}

// ErrBudget 表示迭代超过 4*节点数 的硬预算（可能含互逆规则）。
type ErrBudget struct {
	Rounds, Nodes int
}

func (e ErrBudget) Error() string {
	return fmt.Sprintf("rules: 不动点迭代 %d 轮超过预算 4*%d=%d，疑似规则互逆震荡",
		e.Rounds, e.Nodes, 4*e.Nodes)
}

// Engine 在固定规则集上求不动点。
type Engine struct {
	rules  []Rule
	rounds int
	visits int
	active string // 上一轮产生改写的规则，用于检出互逆活跃
}

// New 构造引擎。规则顺序即应用优先级（见 DESIGN 第 6 节）。
func New(rs []Rule) *Engine { return &Engine{rules: rs} }

// Rounds 返回上一次 Run 的迭代轮数。
func (e *Engine) Rounds() int { return e.rounds }

// Visits 返回上一次 Run 的节点访问次数。
func (e *Engine) Visits() int { return e.visits }

type rframe struct {
	n    ast.Node
	i    int
	kids []ast.Node
}

// rewriteOnce 对树做一遍后序遍历，逐节点尝试规则。
func (e *Engine) rewriteOnce(root ast.Node) (ast.Node, bool) {
	st := []*rframe{{n: root}}
	changed := false
	for len(st) > 0 {
		top := st[len(st)-1]
		if lg, ok := top.n.(ast.Logic); ok && top.i < len(lg.Child) {
			child := lg.Child[top.i]
			top.i++
			st = append(st, &rframe{n: child})
			continue
		}
		cur := top.n
		if lg, ok := top.n.(ast.Logic); ok && len(top.kids) == len(lg.Child) {
			cur = ast.Logic{Kind: lg.Kind, Child: top.kids}
		}
		e.visits++
		for _, rl := range e.rules {
			if nn, ok := rl.Apply(cur); ok {
				cur, changed, e.active = nn, true, rl.Name()
				break
			}
		}
		st = st[:len(st)-1]
		if len(st) > 0 {
			st[len(st)-1].kids = append(st[len(st)-1].kids, cur)
		} else {
			root = cur
		}
	}
	return root, changed
}

// Run 迭代到不动点；超过 4*节点数 预算返回 ErrBudget。
func (e *Engine) Run(root ast.Node) (ast.Node, error) {
	if root == nil {
		return nil, fmt.Errorf("rules: 空谓词树")
	}
	n0 := ast.Count(root)
	e.rounds, e.visits = 0, 0
	for {
		e.rounds++
		if e.rounds > 4*n0 {
			return nil, ErrBudget{Rounds: e.rounds - 1, Nodes: n0}
		}
		var changed bool
		root, changed = e.rewriteOnce(root)
		if !changed {
			return root, nil
		}
	}
}

// BoundOK 报告上一次运行是否满足 访问 ≤ 轮数*节点数*4。
func (e *Engine) BoundOK(root ast.Node) bool {
	n := ast.Count(root)
	if e.rounds == 0 || n == 0 {
		return false
	}
	return e.visits <= e.rounds*n*4
}
