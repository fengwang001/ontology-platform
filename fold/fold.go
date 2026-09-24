// Package fold 在三值（Kleene）逻辑下做保语义的常量折叠与正规形化简。
package fold

import (
	"math/rand"
	"sort"

	"ontology/ast"
	"ontology/rules"
)

// ---- eval-const：常量比较 / 常量 NOT / 常量 AND / OR ----

type evalConst struct{}

func (evalConst) Name() string { return "eval-const" }

func (evalConst) Apply(n ast.Node) (ast.Node, bool) {
	switch q := n.(type) {
	case ast.Logic:
		if q.Kind == "NOT" {
			if c, ok := q.Child[0].(ast.Const); ok {
				return ast.BoolConst(notVal(c.V)), true
			}
			return nil, false
		}
		allConst := true
		for _, c := range q.Child {
			if _, ok := c.(ast.Const); !ok {
				allConst = false
			}
		}
		if !allConst || len(q.Child) == 0 {
			return nil, false
		}
		return ast.BoolConst(q.Eval(nil)), true
	}
	return nil, false
}

func notVal(t ast.Tri) ast.Tri {
	r := ast.Not(ast.BoolConst(t)).Eval(nil)
	return r
}

// ---- flatten + dedup（按规范化串） ----

type flattenDedup struct{}

func (flattenDedup) Name() string { return "flatten-dedup" }

func (flattenDedup) Apply(n ast.Node) (ast.Node, bool) {
	q, ok := n.(ast.Logic)
	if !ok || q.Kind == "NOT" {
		return nil, false
	}
	var flat []ast.Node
	changed := false
	for _, c := range q.Child {
		if lg, ok := c.(ast.Logic); ok && lg.Kind == q.Kind {
			flat, changed = append(flat, lg.Child...), true
		} else {
			flat = append(flat, c)
		}
	}
	sort.SliceStable(flat, func(i, j int) bool { return flat[i].String() < flat[j].String() })
	var uniq []ast.Node
	for _, c := range flat {
		if len(uniq) > 0 && uniq[len(uniq)-1].String() == c.String() {
			changed = true
			continue
		}
		uniq = append(uniq, c)
	}
	if len(uniq) == 1 {
		return uniq[0], true
	}
	if !changed {
		return nil, false
	}
	return ast.Logic{Kind: q.Kind, Child: uniq}, true
}

// ---- absorb：常量吸收（不碰 UNKNOWN），单项退化 ----

type absorb struct{}

func (absorb) Name() string { return "absorb" }

func (absorb) Apply(n ast.Node) (ast.Node, bool) {
	q, ok := n.(ast.Logic)
	if !ok || q.Kind == "NOT" || len(q.Child) == 1 {
		return nil, false
	}
	var keep []ast.Node
	changed := false
	for _, c := range q.Child {
		v, isConst := c.(ast.Const)
		if !isConst {
			keep = append(keep, c)
			continue
		}
		if q.Kind == "AND" && v.V == ast.TFalse || q.Kind == "OR" && v.V == ast.TTrue {
			return ast.BoolConst(v.V), true
		}
		if q.Kind == "AND" && v.V == ast.TTrue || q.Kind == "OR" && v.V == ast.TFalse {
			changed = true
			continue
		}
		keep = append(keep, c) // UNKNOWN 保留
	}
	if !changed {
		return nil, false
	}
	if len(keep) == 0 {
		return ast.BoolConst(neutral(q.Kind)), true
	}
	if len(keep) == 1 {
		return keep[0], true
	}
	return ast.Logic{Kind: q.Kind, Child: keep}, true
}

func neutral(kind string) ast.Tri {
	if kind == "AND" {
		return ast.TTrue
	}
	return ast.TFalse
}

// ---- demorgan：仅单向 NOT(AND/OR) ----

type demorgan struct{}

func (demorgan) Name() string { return "demorgan" }

func (demorgan) Apply(n ast.Node) (ast.Node, bool) {
	q, ok := n.(ast.Logic)
	if !ok || q.Kind != "NOT" {
		return nil, false
	}
	inner, ok := q.Child[0].(ast.Logic)
	if !ok || inner.Kind == "NOT" {
		return nil, false
	}
	kids := make([]ast.Node, len(inner.Child))
	for i, c := range inner.Child {
		kids[i] = ast.Not(c)
	}
	if inner.Kind == "AND" {
		return ast.Or(kids...), true
	}
	return ast.And(kids...), true
}

// Rules 返回默认结构规则（固定顺序，全部为三值恒等）。
func Rules() []rules.Rule {
	return []rules.Rule{evalConst{}, flattenDedup{}, absorb{}, demorgan{}}
}

// ---- seal：偶数 NOT 深度把常量 UNKNOWN 收敛为 FALSE（WHERE 专用） ----

func seal(n ast.Node, depth int) ast.Node {
	switch q := n.(type) {
	case ast.Const:
		if q.V == ast.Unk && depth%2 == 0 {
			return ast.Const{V: ast.TFalse}
		}
		return q
	case ast.Logic:
		kids := make([]ast.Node, len(q.Child))
		nd := depth
		if q.Kind == "NOT" {
			nd++
		}
		for i, c := range q.Child {
			kids[i] = seal(c, nd)
		}
		return ast.Logic{Kind: q.Kind, Child: kids}
	default:
		return n
	}
}

// Fold 先求结构规则不动点，再做 WHERE seal，最后补一轮折叠收敛。
func Fold(root ast.Node) (ast.Node, *rules.Engine, error) {
	eng := rules.New(Rules())
	out, err := eng.Run(root)
	if err != nil {
		return nil, eng, err
	}
	out = seal(out, 0)
	eng2 := rules.New(Rules())
	out, err = eng2.Run(out)
	if err != nil {
		return nil, eng2, err
	}
	return out, eng2, nil
}

// FoldExact 只做三值恒等折叠，不做 WHERE 二值收敛。
func FoldExact(root ast.Node) (ast.Node, error) {
	out, err := rules.New(Rules()).Run(root)
	return out, err
}

// ---- 固定种子随机树生成（供 200 棵等价验证） ----

// RandomTree 用给定 rng 生成一棵深度不超过 d 的随机谓词树。
func RandomTree(r *rand.Rand, d int) ast.Node {
	tables := []string{"a", "b", "c", "d"}
	leaf := func() ast.Node {
		switch r.Intn(4) {
		case 0:
			return ast.BoolConst(ast.Tri(1 + r.Intn(2))) // T/F，不含裸 U
		case 1:
			t := tables[r.Intn(len(tables))]
			return ast.CmpNode(ast.OpEq, ast.ColRef(t, "x"), ast.NullLit())
		case 2:
			t := tables[r.Intn(len(tables))]
			return ast.CmpNode(ast.OpEq, ast.ColRef(t, "x"), ast.IntLit(int64(r.Intn(2))))
		default:
			t := tables[r.Intn(len(tables))]
			return ast.CmpNode(ast.OpEq, ast.ColRef(t, "x"), ast.ColRef(t, "y"))
		}
	}
	if d == 0 || r.Intn(3) == 0 {
		return leaf()
	}
	switch r.Intn(3) {
	case 0:
		return ast.Not(RandomTree(r, d-1))
	case 1:
		k := 2 + r.Intn(2)
		cs := make([]ast.Node, k)
		for i := range cs {
			cs[i] = RandomTree(r, d-1)
		}
		return ast.And(cs...)
	default:
		k := 2 + r.Intn(2)
		cs := make([]ast.Node, k)
		for i := range cs {
			cs[i] = RandomTree(r, d-1)
		}
		return ast.Or(cs...)
	}
}
