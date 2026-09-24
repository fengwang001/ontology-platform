// Package fold 实现常量折叠：精确的三值恒等变换在任何位置应用，
// U→F 类二值折叠（A AND NOT A → FALSE）只在过滤位置应用。
package fold

import "ontology/ast"

// Fold 自顶向下折叠；根节点处于 WHERE 顶层过滤位置。
func Fold(n *ast.Node) *ast.Node { return fold(n, true) }

func fold(n *ast.Node, filterPos bool) *ast.Node {
	switch n.Kind {
	case ast.Not:
		k := fold(n.Kids[0], false) // NOT 之下不再是过滤位置
		if k.Kind == ast.Const {
			return ast.K(ast.Not3(k.Val))
		}
		return ast.Not1(k)
	case ast.Eq:
		a, b := fold(n.Kids[0], false), fold(n.Kids[1], false)
		if a.Kind == ast.Const && b.Kind == ast.Const {
			if a.Val == ast.Unknown || b.Val == ast.Unknown {
				return ast.K(ast.Unknown)
			}
			if a.Val == b.Val {
				return ast.K(ast.True)
			}
			return ast.K(ast.False)
		}
		return ast.Eq2(a, b)
	case ast.And, ast.Or:
		kids := make([]*ast.Node, len(n.Kids))
		for i, k := range n.Kids {
			kids[i] = fold(k, filterPos) // AND/OR 透传过滤位置
		}
		return foldBool(n.Kind, kids, filterPos)
	}
	return n
}

// foldBool 处理 AND/OR：短路、单位元、全常量求值、过滤位置的矛盾折叠。
func foldBool(kind ast.Kind, kids []*ast.Node, filterPos bool) *ast.Node {
	short, unit := ast.K(ast.False), ast.K(ast.True) // AND 的短路值与单位元
	if kind == ast.Or {
		short, unit = ast.K(ast.True), ast.K(ast.False)
	}
	kept := kids[:0]
	allConst := true
	for _, k := range kids {
		if k.Kind == ast.Const {
			if k.Val == short.Val {
				return short // 3VL 下精确：F AND x = F，T OR x = T
			}
			if k.Val == unit.Val {
				continue // 3VL 下精确：T AND x = x，F OR x = x
			}
		} else {
			allConst = false
		}
		kept = append(kept, k)
	}
	if len(kept) == 0 {
		return unit
	}
	if allConst {
		v := kept[0].Val
		for _, k := range kept[1:] {
			if kind == ast.And {
				v = ast.And3(v, k.Val)
			} else {
				v = ast.Or3(v, k.Val)
			}
		}
		return ast.K(v)
	}
	if filterPos && kind == ast.And && hasContradiction(kept) {
		return ast.K(ast.False) // 仅过滤位置：A AND NOT A → FALSE
	}
	if kind == ast.And {
		return ast.AndN(kept...)
	}
	return ast.OrN(kept...)
}

// hasContradiction 检测合取项中同时出现 x 与 NOT x（按规范化打印比对）。
func hasContradiction(kids []*ast.Node) bool {
	set := make(map[string]bool, len(kids))
	for _, k := range kids {
		set[k.String()] = true
	}
	for _, k := range kids {
		if k.Kind == ast.Not && set[k.Kids[0].String()] {
			return true
		}
	}
	return false
}
