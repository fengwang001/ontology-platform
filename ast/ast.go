// Package ast 定义查询谓词树：常量、列引用、比较、NOT/AND/OR。
// 比较与逻辑运算在三值逻辑（Kleene 强三值）下求值。
package ast

import (
	"sort"
	"strings"
)

// Tri 是三值：False < Unknown < True。Unknown 表示 NULL。
type Tri int

const (
	False   Tri = 0
	Unknown Tri = 1
	True    Tri = 2
)

func (t Tri) String() string {
	switch t {
	case True:
		return "TRUE"
	case Unknown:
		return "UNKNOWN"
	default:
		return "FALSE"
	}
}

// Node 是谓词树节点。
type Node interface{ node() }

type Const struct{ V Tri }
type Col struct{ Table, Name string }
type Cmp struct {
	Op   string
	L, R Node
}
type Not struct{ X Node }
type And struct{ Xs []Node }
type Or struct{ Xs []Node }

func (*Const) node() {}
func (*Col) node()   {}
func (*Cmp) node()   {}
func (*Not) node()   {}
func (*And) node()   {}
func (*Or) node()    {}

// Children 返回节点的直接子节点。
func Children(n Node) []Node {
	switch t := n.(type) {
	case *Not:
		return []Node{t.X}
	case *Cmp:
		return []Node{t.L, t.R}
	case *And:
		return t.Xs
	case *Or:
		return t.Xs
	default:
		return nil
	}
}

type frame struct {
	n  Node
	i  int
	op string
}

// Print 返回规范化、确定性的文本表示（迭代遍历，深度 1000 链不溢出）。
func Print(root Node) string {
	if root == nil {
		return "<nil>"
	}
	var b strings.Builder
	st := []frame{{n: root}}
	for len(st) > 0 {
		top := &st[len(st)-1]
		switch t := top.n.(type) {
		case *Const:
			b.WriteString(t.V.String())
			st = st[:len(st)-1]
		case *Col:
			b.WriteString(t.Table + "." + t.Name)
			st = st[:len(st)-1]
		case *Cmp:
			switch top.i {
			case 0:
				b.WriteString("(")
				top.i = 1
				st = append(st, frame{n: t.L})
			case 1:
				b.WriteString(" " + t.Op + " ")
				top.i = 2
				st = append(st, frame{n: t.R})
			default:
				b.WriteString(")")
				st = st[:len(st)-1]
			}
		case *Not:
			if top.i == 0 {
				b.WriteString("NOT(")
				top.i = 1
				st = append(st, frame{n: t.X})
			} else {
				b.WriteString(")")
				st = st[:len(st)-1]
			}
		case *And:
			naryStep(&b, &st, top, t.Xs, "AND")
		case *Or:
			naryStep(&b, &st, top, t.Xs, "OR")
		default:
			st = st[:len(st)-1]
		}
	}
	return b.String()
}

func naryStep(b *strings.Builder, st *[]frame, top *frame, xs []Node, op string) {
	if top.i == 0 {
		b.WriteString("(" + op)
		top.i = 1
	}
	if top.i-1 < len(xs) {
		k := top.i - 1
		b.WriteString(" ")
		top.i++
		*st = append(*st, frame{n: xs[k]})
		return
	}
	b.WriteString(")")
	*st = (*st)[:len(*st)-1]
}

// Count 返回节点总数（迭代）。
func Count(root Node) int {
	n := 0
	st := []Node{root}
	for len(st) > 0 {
		cur := st[len(st)-1]
		st = st[:len(st)-1]
		if cur == nil {
			continue
		}
		n++
		st = append(st, Children(cur)...)
	}
	return n
}

// Tables 返回子树引用到的表名（去重、有序）。
func Tables(root Node) []string {
	seen := map[string]bool{}
	st := []Node{root}
	for len(st) > 0 {
		cur := st[len(st)-1]
		st = st[:len(st)-1]
		if c, ok := cur.(*Col); ok {
			seen[c.Table] = true
		}
		st = append(st, Children(cur)...)
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
