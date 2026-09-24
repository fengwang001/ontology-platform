// Package ast 定义查询谓词树：常量、列引用、比较、AND/OR/NOT，
// 以及三值求值与规范化打印。
package ast

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Tri 是三值逻辑的真值：False < Unknown < True。
type Tri int8

const (
	False Tri = iota
	Unknown
	True
)

func Not3(t Tri) Tri {
	if t == Unknown {
		return Unknown
	}
	return True - t
}

func And3(a, b Tri) Tri { return min(a, b) }
func Or3(a, b Tri) Tri  { return max(a, b) }

// Kind 是节点类别。
type Kind int8

const (
	Bad Kind = iota
	Const
	Column
	Not
	And
	Or
	Eq
)

// Node 是谓词树节点。Const 用 Val；Column 用 Table/Name；其余用 Kids。
type Node struct {
	Kind  Kind
	Val   Tri
	Table string
	Name  string
	Kids  []*Node
}

// ErrEmpty 表示空树（nil），是可判定错误。
var ErrEmpty = errors.New("ast: empty tree")

func K(v Tri) *Node                { return &Node{Kind: Const, Val: v} }
func Col(table, name string) *Node { return &Node{Kind: Column, Table: table, Name: name} }
func Not1(k *Node) *Node           { return &Node{Kind: Not, Kids: []*Node{k}} }
func Eq2(a, b *Node) *Node         { return &Node{Kind: Eq, Kids: []*Node{a, b}} }

// AndN 构造合取；0 个子节点退化为 TRUE，1 个退化为该子节点本身。
func AndN(kids ...*Node) *Node { return boolN(And, kids) }

// OrN 构造析取；0 个子节点退化为 FALSE，1 个退化为该子节点本身。
func OrN(kids ...*Node) *Node { return boolN(Or, kids) }

func boolN(kind Kind, kids []*Node) *Node {
	if len(kids) == 0 {
		if kind == And {
			return K(True)
		}
		return K(False)
	}
	if len(kids) == 1 {
		return kids[0]
	}
	return &Node{Kind: kind, Kids: kids}
}

// Check 做结构校验：空树、非法子节点数、空列名都是可判定错误。
func Check(n *Node) error {
	if n == nil {
		return ErrEmpty
	}
	need := map[Kind]int{Not: 1, Eq: 2}
	switch n.Kind {
	case Const, Column:
		if n.Kind == Column && (n.Table == "" || n.Name == "") {
			return fmt.Errorf("ast: column with empty table or name")
		}
		return nil
	case And, Or:
		if len(n.Kids) < 2 {
			return fmt.Errorf("ast: AND/OR with %d kids", len(n.Kids))
		}
	case Not, Eq:
		if len(n.Kids) != need[n.Kind] {
			return fmt.Errorf("ast: bad kid count %d", len(n.Kids))
		}
	default:
		return fmt.Errorf("ast: bad kind %d", n.Kind)
	}
	for _, k := range n.Kids {
		if err := Check(k); err != nil {
			return err
		}
	}
	return nil
}

// Size 返回节点总数。
func (n *Node) Size() int {
	s := 1
	for _, k := range n.Kids {
		s += k.Size()
	}
	return s
}

// Eval 在赋值 env（键 "table.name"）下求三值结果；缺省列按 Unknown 处理。
func (n *Node) Eval(env map[string]Tri) Tri {
	switch n.Kind {
	case Const:
		return n.Val
	case Column:
		return env[n.Table+"."+n.Name]
	case Not:
		return Not3(n.Kids[0].Eval(env))
	case And, Or:
		v := True - Tri(n.Kind-And)*2 // AND 的单位元是 T，OR 是 F
		op := And3
		if n.Kind == Or {
			op = Or3
		}
		for _, k := range n.Kids {
			v = op(v, k.Eval(env))
		}
		return v
	case Eq:
		a, b := n.Kids[0].Eval(env), n.Kids[1].Eval(env)
		if a == Unknown || b == Unknown {
			return Unknown
		}
		if a == b {
			return True
		}
		return False
	}
	return Unknown
}

// String 输出规范化形式：AND/OR/EQ 的子节点按字典序排序，保证同构树
// （交换律意义下）打印逐字节相同。
func (n *Node) String() string {
	switch n.Kind {
	case Const:
		return []string{"FALSE", "NULL", "TRUE"}[n.Val]
	case Column:
		return n.Table + "." + n.Name
	case Not:
		return "NOT(" + n.Kids[0].String() + ")"
	}
	parts := make([]string, len(n.Kids))
	for i, k := range n.Kids {
		parts[i] = k.String()
	}
	slices.Sort(parts)
	return []string{"AND", "OR", "EQ"}[n.Kind-And] + "(" + strings.Join(parts, ", ") + ")"
}

// Cols 返回引用的列（"table.name"），排序去重。
func (n *Node) Cols() []string {
	set := map[string]bool{}
	var walk func(m *Node)
	walk = func(m *Node) {
		if m.Kind == Column {
			set[m.Table+"."+m.Name] = true
		}
		for _, k := range m.Kids {
			walk(k)
		}
	}
	walk(n)
	return slices.Sorted(maps.Keys(set))
}

// Tables 返回引用的表，排序去重。
func (n *Node) Tables() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range n.Cols() {
		if t, _, _ := strings.Cut(c, "."); !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
