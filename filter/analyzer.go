package filter

import (
	"strconv"

	"ontology/policy"
	"ontology/predicate"
)

type analyzer struct {
	pol     *policy.Policy
	role    string
	count   int
	hidden  []Ref
	unknown []Ref
	invalid bool
}

// walk 是唯一的谓词遍历入口，自底向上；返回折叠后的节点与“是否常量”。
func (a *analyzer) walk(n predicate.Node, path string) (predicate.Node, bool) {
	a.count++
	switch t := n.(type) {
	case nil:
		return predicate.Const{Value: true}, true
	case predicate.Const:
		return t, true
	case predicate.Cmp:
		a.classify(t.Col, path+"/cmp")
		return t, false
	case predicate.Not:
		x, isConst := a.walk(t.X, path+"/not")
		if !isConst {
			return predicate.Not{X: x}, false
		}
		return predicate.Const{Value: !x.(predicate.Const).Value}, true
	case predicate.And:
		if len(t.Xs) == 0 {
			a.invalid = true
			return t, false
		}
		return a.junction(t.Xs, path, true)
	case predicate.Or:
		if len(t.Xs) == 0 {
			a.invalid = true
			return t, false
		}
		return a.junction(t.Xs, path, false)
	default:
		a.invalid = true
		return n, false
	}
}

func (a *analyzer) junction(xs []predicate.Node, path string, isAnd bool) (predicate.Node, bool) {
	h0, u0 := len(a.hidden), len(a.unknown)
	live := make([]predicate.Node, 0, len(xs))
	absorbing := false
	for i, x := range xs {
		branch := path
		if isAnd {
			branch += indexPath("/and", i)
		} else {
			branch += indexPath("/or", i)
		}
		f, isConst := a.walk(x, branch)
		if !isConst {
			live = append(live, f)
			continue
		}
		v := f.(predicate.Const).Value
		switch {
		case isAnd && !v:
			absorbing = true
		case !isAnd && v:
			absorbing = true
		}
	}
	if absorbing {
		// 吸收常量使整支成为常量：其余子树运行时不求值，
		// 其中收集到的隐藏/未知列引用一律视为未被读取。
		a.hidden = a.hidden[:h0]
		a.unknown = a.unknown[:u0]
		return predicate.Const{Value: !isAnd}, true
	}
	if len(live) == 0 {
		return predicate.Const{Value: isAnd}, true
	}
	if isAnd {
		return predicate.And{Xs: live}, false
	}
	return predicate.Or{Xs: live}, false
}

func indexPath(prefix string, i int) string {
	return prefix + "[" + strconv.Itoa(i) + "]"
}

func (a *analyzer) classify(col, path string) {
	switch {
	case !a.pol.Knows(col):
		a.unknown = append(a.unknown, Ref{Col: col, Path: path})
	case !a.pol.IsVisible(a.role, col):
		a.hidden = append(a.hidden, Ref{Col: col, Path: path})
	}
}
