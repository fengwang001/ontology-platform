// Package ast defines predicate trees over three-valued logic.
package ast

import (
	"fmt"
	"sort"
	"strings"
)

// Value is a three-valued truth value.
type Value int

const (
	Unknown Value = iota
	False
	True
)

func (v Value) String() string {
	return [...]string{"unk", "false", "true"}[v]
}

// Kind classifies a Node.
type Kind int

const (
	KConst Kind = iota
	KColumn
	KCmp
	KAnd
	KOr
	KNot
)

// Node is a predicate tree node. Kids holds operands: 2 for KCmp,
// 1 for KNot, any number for KAnd/KOr.
type Node struct {
	Kind  Kind
	Val   Value  // KConst
	Table string // KColumn
	Name  string // KColumn
	Op    string // KCmp: "=" or "<>"
	Kids  []*Node
}

func Const(v Value) *Node          { return &Node{Kind: KConst, Val: v} }
func Col(table, name string) *Node { return &Node{Kind: KColumn, Table: table, Name: name} }
func Not(k *Node) *Node            { return &Node{Kind: KNot, Kids: []*Node{k}} }
func And(kids ...*Node) *Node      { return &Node{Kind: KAnd, Kids: kids} }
func Or(kids ...*Node) *Node       { return &Node{Kind: KOr, Kids: kids} }
func Eq(a, b *Node) *Node          { return &Node{Kind: KCmp, Op: "=", Kids: []*Node{a, b}} }
func Ne(a, b *Node) *Node          { return &Node{Kind: KCmp, Op: "<>", Kids: []*Node{a, b}} }

// Key is the canonical column reference.
func Key(table, name string) string { return table + "." + name }

// Eval evaluates n under env (column key -> value); missing keys are Unknown.
func (n *Node) Eval(env map[string]Value) Value {
	switch n.Kind {
	case KConst:
		return n.Val
	case KColumn:
		return env[Key(n.Table, n.Name)]
	case KCmp:
		l, r := n.Kids[0].Eval(env), n.Kids[1].Eval(env)
		if l == Unknown || r == Unknown {
			return Unknown
		}
		eq := l == r
		if n.Op == "<>" {
			eq = !eq
		}
		if eq {
			return True
		}
		return False
	case KAnd:
		out := True
		for _, k := range n.Kids {
			switch v := k.Eval(env); v {
			case False:
				return False
			case Unknown:
				out = Unknown
			}
		}
		return out
	case KOr:
		out := False
		for _, k := range n.Kids {
			switch v := k.Eval(env); v {
			case True:
				return True
			case Unknown:
				out = Unknown
			}
		}
		return out
	case KNot:
		switch v := n.Kids[0].Eval(env); v {
		case True:
			return False
		case False:
			return True
		}
		return Unknown
	}
	return Unknown
}

// String prints the normalized form; And/Or kids are sorted for determinism.
func (n *Node) String() string {
	switch n.Kind {
	case KConst:
		return n.Val.String()
	case KColumn:
		return Key(n.Table, n.Name)
	case KCmp:
		return "(" + n.Op + " " + n.Kids[0].String() + " " + n.Kids[1].String() + ")"
	case KNot:
		return "(not " + n.Kids[0].String() + ")"
	}
	op := "and"
	if n.Kind == KOr {
		op = "or"
	}
	parts := make([]string, len(n.Kids))
	for i, k := range n.Kids {
		parts[i] = k.String()
	}
	sort.Strings(parts)
	return "(" + op + " " + strings.Join(parts, " ") + ")"
}

// Size counts nodes.
func Size(n *Node) int {
	s := 1
	for _, k := range n.Kids {
		s += Size(k)
	}
	return s
}

// Columns returns the sorted distinct column keys of all given trees.
func Columns(ns ...*Node) []string {
	set := map[string]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Kind == KColumn {
			set[Key(n.Table, n.Name)] = true
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	for _, n := range ns {
		walk(n)
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Tables returns the set of tables referenced by n.
func Tables(n *Node) map[string]bool {
	set := map[string]bool{}
	for _, c := range Columns(n) {
		set[strings.SplitN(c, ".", 2)[0]] = true
	}
	return set
}

// Validate reports structural errors (nil node, bad arity, empty column).
func Validate(n *Node) error {
	if n == nil {
		return fmt.Errorf("empty tree")
	}
	need := map[Kind]int{KCmp: 2, KNot: 1}
	if w, ok := need[n.Kind]; ok && len(n.Kids) != w {
		return fmt.Errorf("kind %d wants %d kids, got %d", n.Kind, w, len(n.Kids))
	}
	if (n.Kind == KAnd || n.Kind == KOr) && len(n.Kids) == 0 {
		return fmt.Errorf("kind %d with no kids", n.Kind)
	}
	if n.Kind == KColumn && (n.Table == "" || n.Name == "") {
		return fmt.Errorf("column with empty table or name")
	}
	for _, k := range n.Kids {
		if err := Validate(k); err != nil {
			return err
		}
	}
	return nil
}
