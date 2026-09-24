// Package ast 定义查询谓词树、三值求值、规范化打印与查询计划节点。
package ast

import (
	"fmt"
	"sort"
	"strings"
)

// Tri 是 Kleene 三值逻辑真值。
type Tri int8

const (
	Unk Tri = iota // 未知（NULL 比较结果）
	TTrue
	TFalse
)

func (t Tri) String() string {
	switch t {
	case TTrue:
		return "TRUE"
	case TFalse:
		return "FALSE"
	default:
		return "UNKNOWN"
	}
}

// Value 为小整数域上的值；Null=true 表示 SQL NULL。
type Value struct {
	N    int64
	Null bool
}

// Env 是列（"表.列"）到值的赋值。
type Env map[string]Value

// Node 是谓词树节点。
type Node interface {
	Eval(Env) Tri
	String() string
}

// Const 为常量真值。
type Const struct{ V Tri }

func (c Const) Eval(Env) Tri { return c.V }
func (c Const) String() string {
	if c.V == TTrue {
		return "TRUE"
	} else if c.V == TFalse {
		return "FALSE"
	}
	return "UNKNOWN"
}

// Ref 为列引用；直接作为布尔原子求值时，NULL/缺失列给未知。
type Ref struct{ Table, Col string }

func (r Ref) Eval(e Env) Tri {
	v, ok := e[r.Table+"."+r.Col]
	if !ok || v.Null {
		return Unk
	}
	return TTrue
}
func (r Ref) String() string { return r.Table + "." + r.Col }

// CmpOp 为比较算子。
type CmpOp string

const (
	OpEq CmpOp = "="
	OpLt CmpOp = "<"
)

// Operand 为比较的一侧：列引用或小整数常量（含 NULL）。
type Operand struct {
	Ref     *Ref
	Lit     int64
	IsLit   bool
	LitNull bool
}

// Cmp 为比较谓词；任一操作数为 NULL 或列缺失则结果未知。
type Cmp struct {
	Op   CmpOp
	L, R Operand
}

func operandVal(o Operand, e Env) (Value, bool) {
	if o.IsLit {
		return Value{N: o.Lit, Null: o.LitNull}, true
	}
	v, ok := e[o.Ref.Table+"."+o.Ref.Col]
	return v, ok
}

func cmpTri(op CmpOp, a, b int64) Tri {
	switch op {
	case OpEq:
		if a == b {
			return TTrue
		}
	case OpLt:
		if a < b {
			return TTrue
		}
	}
	return TFalse
}

func (c Cmp) Eval(e Env) Tri {
	a, ok1 := operandVal(c.L, e)
	b, ok2 := operandVal(c.R, e)
	if !ok1 || !ok2 || a.Null || b.Null {
		return Unk
	}
	return cmpTri(c.Op, a.N, b.N)
}

func opndString(o Operand) string {
	switch {
	case o.IsLit && o.LitNull:
		return "NULL"
	case o.IsLit:
		return fmt.Sprintf("%d", o.Lit)
	default:
		return o.Ref.String()
	}
}

func (c Cmp) String() string {
	return "(" + opndString(c.L) + " " + string(c.Op) + " " + opndString(c.R) + ")"
}

// Logic 为 AND/OR/NOT。Kind ∈ {"AND","OR","NOT"}。
type Logic struct {
	Kind  string
	Child []Node
}

func (l Logic) Eval(e Env) Tri {
	if l.Kind == "NOT" {
		return notTri(l.Child[0].Eval(e))
	}
	acc := TTrue
	if l.Kind == "OR" {
		acc = TFalse
	}
	for _, c := range l.Child {
		if l.Kind == "AND" {
			acc = andTri(acc, c.Eval(e))
		} else {
			acc = orTri(acc, c.Eval(e))
		}
	}
	return acc
}

func (l Logic) String() string {
	if l.Kind == "NOT" {
		return "(NOT " + l.Child[0].String() + ")"
	}
	parts := make([]string, len(l.Child))
	for i, c := range l.Child {
		parts[i] = c.String()
	}
	sort.Strings(parts) // 规范化：合取/析取项顺序无关
	return "(" + l.Kind + " " + strings.Join(parts, " ") + ")"
}
