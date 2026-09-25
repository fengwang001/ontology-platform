package api

import (
	"fmt"
	"sort"
)

// Predicate 是查询谓词。Columns 返回引用的全部列（去重升序），
// 供查询前做可见性校验；match 只在全部列可见后才会被调用。
type Predicate interface {
	Columns() []string
	match(obj map[string]any) bool
}

// Cmp 是列与常量的比较，Op 支持 = != > >= < <=。
type Cmp struct {
	Column string
	Op     string
	Value  any
}

func (c Cmp) Columns() []string { return []string{c.Column} }

func (c Cmp) match(obj map[string]any) bool {
	v, ok := obj[c.Column]
	if !ok {
		return false
	}
	return compare(v, c.Value, c.Op)
}

// IsNull 判定列是否缺失或为 nil。
type IsNull struct{ Column string }

func (n IsNull) Columns() []string { return []string{n.Column} }

func (n IsNull) match(obj map[string]any) bool {
	v, ok := obj[n.Column]
	return !ok || v == nil
}

// Not 是否定。
type Not struct{ Inner Predicate }

func (n Not) Columns() []string { return n.Inner.Columns() }

func (n Not) match(obj map[string]any) bool { return !n.Inner.match(obj) }

// And 是合取。
type And struct{ Parts []Predicate }

func (a And) Columns() []string { return unionColumns(a.Parts) }

func (a And) match(obj map[string]any) bool {
	for _, p := range a.Parts {
		if !p.match(obj) {
			return false
		}
	}
	return true
}

// Or 是析取。
type Or struct{ Parts []Predicate }

func (o Or) Columns() []string { return unionColumns(o.Parts) }

func (o Or) match(obj map[string]any) bool {
	for _, p := range o.Parts {
		if p.match(obj) {
			return true
		}
	}
	return false
}

func unionColumns(parts []Predicate) []string {
	set := make(map[string]bool)
	for _, p := range parts {
		for _, c := range p.Columns() {
			set[c] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func compare(a, b any, op string) bool {
	if fa, fb, ok := asFloats(a, b); ok {
		switch op {
		case "=":
			return fa == fb
		case "!=":
			return fa != fb
		case ">":
			return fa > fb
		case ">=":
			return fa >= fb
		case "<":
			return fa < fb
		case "<=":
			return fa <= fb
		}
		return false
	}
	sa, sb := fmt.Sprint(a), fmt.Sprint(b)
	switch op {
	case "=":
		return sa == sb
	case "!=":
		return sa != sb
	case ">":
		return sa > sb
	case ">=":
		return sa >= sb
	case "<":
		return sa < sb
	case "<=":
		return sa <= sb
	}
	return false
}

func asFloats(a, b any) (float64, float64, bool) {
	fa, okA := toFloat(a)
	fb, okB := toFloat(b)
	return fa, fb, okA && okB
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
