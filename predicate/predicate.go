// Package predicate 定义过滤谓词树：列比较、AND、OR、NOT 与常量。
package predicate

import (
	"errors"
	"fmt"
)

// ErrInvalid 表示谓词本身非法（空列名、未知算符、nil 子节点）。
var ErrInvalid = errors.New("predicate: invalid predicate")

// Node 是谓词树节点。实现：Const、Cmp、And、Or、Not。
type Node interface{ node() }

// Const 是常量布尔节点（永真/永假），用于常量折叠。
type Const struct{ Value bool }

// Cmp 是列比较节点：Column Op Value，例如 secret = 1、secret IS NULL。
type Cmp struct {
	Column string
	Op     string
	Value  any
}

// And 是合取节点。
type And struct{ L, R Node }

// Or 是析取节点。
type Or struct{ L, R Node }

// Not 是否定节点。
type Not struct{ X Node }

func (Const) node() {}
func (Cmp) node()   {}
func (And) node()   {}
func (Or) node()    {}
func (Not) node()   {}

// Ops 是支持的比较算符。
var Ops = map[string]bool{
	"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
	"IS NULL": true, "IS NOT NULL": true,
}

// Count 返回谓词树的节点总数；nil 记 0。
func Count(n Node) int {
	switch t := n.(type) {
	case nil:
		return 0
	case Const, Cmp:
		return 1
	case Not:
		return 1 + Count(t.X)
	case And:
		return 1 + Count(t.L) + Count(t.R)
	case Or:
		return 1 + Count(t.L) + Count(t.R)
	default:
		return 0
	}
}

// Validate 校验谓词结构合法；nil 视为「无过滤」，合法。
func Validate(n Node) error {
	switch t := n.(type) {
	case nil:
		return nil
	case Const:
		return nil
	case Cmp:
		if t.Column == "" {
			return fmt.Errorf("%w: empty column", ErrInvalid)
		}
		if !Ops[t.Op] {
			return fmt.Errorf("%w: unknown op %q", ErrInvalid, t.Op)
		}
		return nil
	case Not:
		if t.X == nil {
			return fmt.Errorf("%w: nil operand of NOT", ErrInvalid)
		}
		return Validate(t.X)
	case And:
		if t.L == nil || t.R == nil {
			return fmt.Errorf("%w: nil operand of AND", ErrInvalid)
		}
		if err := Validate(t.L); err != nil {
			return err
		}
		return Validate(t.R)
	case Or:
		if t.L == nil || t.R == nil {
			return fmt.Errorf("%w: nil operand of OR", ErrInvalid)
		}
		if err := Validate(t.L); err != nil {
			return err
		}
		return Validate(t.R)
	default:
		return fmt.Errorf("%w: unknown node %T", ErrInvalid, n)
	}
}
