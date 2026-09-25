// Package dep 维护列声明与依赖图：校验、环检测、传递依赖闭包。
// 本包不依赖工程内其他包。
package dep

import "errors"

// 构建期可判定的哨兵错误。
var (
	ErrDuplicateColumn = errors.New("dep: duplicate column")
	ErrUndeclaredDep   = errors.New("dep: dependency on undeclared column")
	ErrCyclic          = errors.New("dep: cyclic dependency")
)

// Column 是列声明：Base=true 为基列（用 Initial），否则为派生列
// （用 Deps 与 Fn；Fn 入参按 Deps 顺序给出各依赖当前值）。
type Column struct {
	Name    string
	Base    bool
	Initial int
	Deps    []string
	Fn      func(vals []int) int
}

// Spec 是整张表的列声明。
type Spec struct{ Columns []Column }

// Graph 是校验通过后的不可变依赖图。
type Graph struct {
	order      []string
	initial    map[string]int
	deps       map[string][]string
	dependents map[string][]string
	fns        map[string]func([]int) int
}

// Build 校验声明并构图；重名、依赖未声明列或有环时在任何产出前整体失败。
func Build(spec Spec) (*Graph, error) {
	order := make([]string, 0, len(spec.Columns))
	seen := map[string]bool{}
	for _, c := range spec.Columns {
		if seen[c.Name] {
			return nil, ErrDuplicateColumn
		}
		seen[c.Name] = true
		order = append(order, c.Name)
	}
	g := &Graph{
		order:      order,
		initial:    map[string]int{},
		deps:       map[string][]string{},
		dependents: map[string][]string{},
		fns:        map[string]func([]int) int{},
	}
	for _, c := range spec.Columns {
		if c.Base {
			g.initial[c.Name] = c.Initial
			continue
		}
		g.deps[c.Name] = append([]string(nil), c.Deps...)
		g.fns[c.Name] = c.Fn
		for _, d := range c.Deps {
			if !seen[d] {
				return nil, ErrUndeclaredDep
			}
			g.dependents[d] = append(g.dependents[d], c.Name)
		}
	}
	if err := checkAcyclic(g); err != nil {
		return nil, err
	}
	return g, nil
}

// checkAcyclic 用三色 DFS 检测「派生列 -> 依赖」方向上的环。
func checkAcyclic(g *Graph) error {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var visit func(n string) int
	visit = func(n string) int {
		if color[n] != white {
			if color[n] == gray {
				return -1
			}
			return 0
		}
		color[n] = gray
		for _, d := range g.deps[n] {
			if visit(d) < 0 {
				return -1
			}
		}
		color[n] = black
		return 0
	}
	for _, n := range g.order {
		if visit(n) < 0 {
			return ErrCyclic
		}
	}
	return nil
}

// Has 报告列是否已声明。
func (g *Graph) Has(name string) bool {
	if _, ok := g.initial[name]; ok {
		return true
	}
	_, ok := g.deps[name]
	return ok
}

// IsDerived 报告列是否为派生列。
func (g *Graph) IsDerived(name string) bool { _, ok := g.deps[name]; return ok }

// Initial 返回基列初值。
func (g *Graph) Initial(name string) int { return g.initial[name] }

// Deps 返回派生列的依赖列名（声明顺序）。
func (g *Graph) Deps(name string) []string { return g.deps[name] }

// Fn 返回派生列的计算函数。
func (g *Graph) Fn(name string) func([]int) int { return g.fns[name] }

// Names 返回全部声明列名（声明顺序）。
func (g *Graph) Names() []string { return append([]string(nil), g.order...) }

// DependentsClosure 返回所有（传递）依赖 root 的派生列（BFS 顺序，不含 root）。
func (g *Graph) DependentsClosure(root string) []string {
	got := map[string]bool{}
	out := []string{}
	frontier := append([]string(nil), g.dependents[root]...)
	for len(frontier) > 0 {
		n := frontier[0]
		frontier = frontier[1:]
		if got[n] {
			continue
		}
		got[n] = true
		out = append(out, n)
		frontier = append(frontier, g.dependents[n]...)
	}
	return out
}
