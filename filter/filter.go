// Package filter 按列级可见策略裁剪行与谓词；谓词读取不可见列时整条拒绝（丙方案）。
package filter

import (
	"errors"
	"fmt"

	"ontology/policy"
	"ontology/predicate"
)

var (
	// ErrHiddenColumn：谓词读取了对该角色不可见的列。
	ErrHiddenColumn = errors.New("predicate references hidden column")
	// ErrUnknownColumn：谓词引用了策略全集之外的列。
	ErrUnknownColumn = errors.New("predicate references unknown column")
	// ErrInvalidPredicate：谓词结构非法。
	ErrInvalidPredicate = errors.New("invalid predicate structure")
)

// Ref 是一次被拒绝的谓词引用。
type Ref struct {
	Col  string
	Path string
}

// RejectError 是整条拒绝的可判定错误，可用 errors.Is 归入三类哨兵之一。
type RejectError struct {
	Kind error
	Refs []Ref
}

func (e *RejectError) Error() string {
	if len(e.Refs) == 0 {
		return e.Kind.Error()
	}
	return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Refs[0].Col, e.Refs[0].Path)
}

func (e *RejectError) Unwrap() error { return e.Kind }

// Compiled 是通过可见性检查的查询计划。
type Compiled struct {
	pol      *policy.Policy
	role     string
	pred     predicate.Node
	visible  []string
	visits   int
	copyHits int
}

// Compile 一遍自底向上遍历谓词：计数节点、折叠常量、收集被实际读取的列引用。
// 常量子树内的不可见列视为未被读取（OR 短路例外，对称适用于 AND/NOT）。
func Compile(pol *policy.Policy, role string, pred predicate.Node) (*Compiled, error) {
	a := &analyzer{pol: pol, role: role}
	folded, isConst := a.walk(pred, "$")
	if len(a.unknown) > 0 {
		return nil, &RejectError{Kind: ErrUnknownColumn, Refs: a.unknown}
	}
	if len(a.hidden) > 0 {
		return nil, &RejectError{Kind: ErrHiddenColumn, Refs: a.hidden}
	}
	if a.invalid {
		return nil, &RejectError{Kind: ErrInvalidPredicate}
	}
	if isConst && !folded.(predicate.Const).Value {
		folded = predicate.Const{Value: false}
	}
	return &Compiled{
		pol:     pol,
		role:    role,
		pred:    folded,
		visible: pol.Visible(role),
		visits:  a.count,
	}, nil
}

// NodesVisited 返回编译期遍历过的谓词节点数。
func (c *Compiled) NodesVisited() int { return c.visits }

// CopyOps 返回行裁剪至今发生的可见列拷贝次数。
func (c *Compiled) CopyOps() int { return c.copyHits }

// Project 仅按可见列投影单行：不可见列从 map 中移除（不置零、不置 NULL）。
func (c *Compiled) Project(row map[string]any) map[string]any {
	out := make(map[string]any, len(c.visible))
	for _, col := range c.visible {
		v, ok := row[col]
		if !ok {
			continue
		}
		c.copyHits++
		out[col] = v
	}
	return out
}

// Apply 过滤并投影多行；谓词对投影后的行求值。
func (c *Compiled) Apply(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		projected := c.Project(row)
		if !predicate.Eval(c.pred, projected) {
			continue
		}
		out = append(out, projected)
	}
	return out
}
