// Package rules 定义重写规则集，并以不动点迭代驱动重写，
// 附带迭代轮数 / 节点访问计数器与震荡检测。
package rules

import (
	"errors"
	"math/rand"
	"strings"

	"ontology/ast"
	"ontology/fold"
)

// Rule 是一条重写规则：Local 为节点级局部重写，Whole 为整树变换，
// 二者必居其一。
type Rule struct {
	Name  string
	Local func(*ast.Node) (*ast.Node, bool)
	Whole func(*ast.Node) *ast.Node
}

// Default 返回默认规则集：常量折叠（含过滤位置受限折叠）、双重否定
// 消除、德摩根两条。规则集不含互逆规则。
func Default() []Rule {
	return []Rule{
		{Name: "const-fold", Whole: fold.Fold},
		{Name: "double-negation", Local: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.Not && n.Kids[0].Kind == ast.Not {
				return n.Kids[0].Kids[0], true
			}
			return nil, false
		}},
		{Name: "de-morgan-and", Local: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.Not && n.Kids[0].Kind == ast.And {
				kids := make([]*ast.Node, len(n.Kids[0].Kids))
				for i, k := range n.Kids[0].Kids {
					kids[i] = ast.Not1(k)
				}
				return ast.OrN(kids...), true
			}
			return nil, false
		}},
		{Name: "de-morgan-or", Local: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.Not && n.Kids[0].Kind == ast.Or {
				kids := make([]*ast.Node, len(n.Kids[0].Kids))
				for i, k := range n.Kids[0].Kids {
					kids[i] = ast.Not1(k)
				}
				return ast.AndN(kids...), true
			}
			return nil, false
		}},
	}
}

// Stats 记录重写过程计数。
type Stats struct {
	Rounds int // 不动点迭代轮数
	Visits int // 节点访问总次数
}

var (
	// ErrOscillation 表示检测到震荡（规则集含互逆规则）。
	ErrOscillation = errors.New("rules: oscillation detected (inverse rules?)")
	// ErrNoConverge 表示迭代轮数超过 4*节点数 的上界。
	ErrNoConverge = errors.New("rules: iteration bound exceeded")
)

// Rewrite 对 n 反复应用规则直到不动点。空树报 ast.ErrEmpty；
// 同一形态复现报 ErrOscillation；超过轮数上界报 ErrNoConverge。
func Rewrite(n *ast.Node, rs []Rule) (*ast.Node, Stats, error) {
	var st Stats
	if err := ast.Check(n); err != nil {
		return nil, st, err
	}
	bound := 4*n.Size() + 1
	seen := map[string]bool{}
	for {
		s := n.String()
		if seen[s] {
			return nil, st, ErrOscillation
		}
		seen[s] = true
		var changed bool
		n, changed = pass(n, rs, &st)
		st.Rounds++
		if !changed {
			return n, st, nil
		}
		if st.Rounds > bound {
			return nil, st, ErrNoConverge
		}
	}
}

// pass 单轮遍历：先按序应用 Whole 规则，再自底向上应用 Local 规则
// （同一节点重复尝试直至无规则命中，上限 16 次，防止互逆规则死循环）。
func pass(n *ast.Node, rs []Rule, st *Stats) (*ast.Node, bool) {
	changed := false
	for _, r := range rs {
		if r.Whole == nil {
			continue
		}
		st.Visits += n.Size()
		if m := r.Whole(n); m.String() != n.String() {
			n, changed = m, true
		}
	}
	var walk func(x *ast.Node) *ast.Node
	walk = func(x *ast.Node) *ast.Node {
		st.Visits++
		kids := make([]*ast.Node, len(x.Kids))
		for i, k := range x.Kids {
			kids[i] = walk(k)
		}
		x = &ast.Node{Kind: x.Kind, Val: x.Val, Table: x.Table, Name: x.Name, Kids: kids}
		for cap := 0; cap < 16; cap++ {
			fired := false
			for _, r := range rs {
				if r.Local == nil {
					continue
				}
				if m, ok := r.Local(x); ok {
					x, fired, changed = m, true, true
					break
				}
			}
			if !fired {
				break
			}
		}
		return x
	}
	return walk(n), changed
}

// GenRandom 用固定种子的 rand 生成随机谓词树，供测试与演示使用。
func GenRandom(r *rand.Rand, cols []string, depth int) *ast.Node {
	if depth == 0 || r.Intn(4) == 0 {
		if r.Intn(3) == 0 {
			return ast.K(ast.Tri(r.Intn(3)))
		}
		t, name, _ := strings.Cut(cols[r.Intn(len(cols))], ".")
		return ast.Col(t, name)
	}
	switch r.Intn(4) {
	case 0:
		return ast.Not1(GenRandom(r, cols, depth-1))
	case 1:
		return ast.Eq2(GenRandom(r, cols, depth-1), GenRandom(r, cols, depth-1))
	case 2:
		return ast.AndN(GenRandom(r, cols, depth-1), GenRandom(r, cols, depth-1))
	default:
		return ast.OrN(GenRandom(r, cols, depth-1), GenRandom(r, cols, depth-1))
	}
}
