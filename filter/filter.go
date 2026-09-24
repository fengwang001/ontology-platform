// Package filter 按策略检查谓词并裁剪行：谓词引用不可见列即整条拒绝。
package filter

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/policy"
	"ontology/predicate"
)

// ErrInvisibleColumn 表示谓词引用了对该角色不可见的列，查询被整条拒绝。
var ErrInvisibleColumn = errors.New("filter: predicate references invisible column")

// Ref 是一处被拒的谓词引用：列名 + 它在谓词树中出现的全部路径。
type Ref struct {
	Column string
	Paths  []string
}

// RefError 携带本次查询全部不可见列引用；errors.Is(err, ErrInvisibleColumn) 为真。
type RefError struct{ Refs []Ref }

func (e *RefError) Error() string {
	parts := make([]string, 0, len(e.Refs))
	for _, r := range e.Refs {
		parts = append(parts, fmt.Sprintf("%s at %s", r.Column, strings.Join(r.Paths, ",")))
	}
	return ErrInvisibleColumn.Error() + ": " + strings.Join(parts, "; ")
}

// Is 使 RefError 可用 errors.Is(_, ErrInvisibleColumn) 判定。
func (e *RefError) Is(target error) bool { return target == ErrInvisibleColumn }

// Row 是一行数据：列名到值。
type Row map[string]any

// Engine 是检查与裁剪引擎。visited/copies 为非导出计数器，用于复杂度审计。
type Engine struct {
	pol     *policy.Policy
	visited int
	copies  int
}

// NewEngine 基于策略 p 构造引擎。
func NewEngine(p *policy.Policy) *Engine { return &Engine{pol: p} }

// Visited 返回最近一次 Check 访问过的谓词节点数。
func (e *Engine) Visited() int { return e.visited }

// Copies 返回最近一次 PruneRow 的列拷贝次数。
func (e *Engine) Copies() int { return e.copies }

type fold int8

const (
	foldUnknown fold = iota
	foldFalse
	foldTrue
)

// Check 单遍从左到右检查谓词：不可见列被实际读取则返回全部引用与 *RefError。
// nil 谓词表示无过滤，直接放行。OR/AND 按短路求值做常量折叠（见 DESIGN.md）。
func (e *Engine) Check(role string, n predicate.Node) ([]Ref, error) {
	e.visited = 0
	if err := predicate.Validate(n); err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	cols, err := e.pol.Columns(role)
	if err != nil {
		return nil, err
	}
	vis := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		vis[c] = struct{}{}
	}
	found := map[string][]string{}
	e.eval(n, vis, "$", found)
	if len(found) == 0 {
		return nil, nil
	}
	refs := make([]Ref, 0, len(found))
	for col, paths := range found {
		refs = append(refs, Ref{Column: col, Paths: paths})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Column < refs[j].Column })
	return refs, &RefError{Refs: refs}
}

func (e *Engine) eval(n predicate.Node, vis map[string]struct{}, path string, refs map[string][]string) fold {
	e.visited++
	switch t := n.(type) {
	case predicate.Const:
		if t.Value {
			return foldTrue
		}
		return foldFalse
	case predicate.Cmp:
		if _, ok := vis[t.Column]; !ok {
			refs[t.Column] = append(refs[t.Column], path)
		}
		return foldUnknown
	case predicate.Not:
		switch e.eval(t.X, vis, path+".not", refs) {
		case foldTrue:
			return foldFalse
		case foldFalse:
			return foldTrue
		}
		return foldUnknown
	case predicate.And:
		lf := e.eval(t.L, vis, path+".and.l", refs)
		if lf == foldFalse {
			return foldFalse
		}
		rf := e.eval(t.R, vis, path+".and.r", refs)
		if lf == foldTrue {
			return rf
		}
		if rf == foldFalse {
			return foldFalse
		}
		return foldUnknown
	case predicate.Or:
		lf := e.eval(t.L, vis, path+".or.l", refs)
		if lf == foldTrue {
			return foldTrue
		}
		rf := e.eval(t.R, vis, path+".or.r", refs)
		if lf == foldFalse {
			return rf
		}
		if rf == foldTrue {
			return foldTrue
		}
		return foldUnknown
	}
	return foldUnknown
}

// PruneRow 返回只含可见列的新行；不可见列被移除（不置零值/NULL）。
// 代价与可见列数成正比：只遍历可见列集合，不遍历整行。
func (e *Engine) PruneRow(role string, row Row) (Row, error) {
	e.copies = 0
	cols, err := e.pol.Columns(role)
	if err != nil {
		return nil, err
	}
	out := make(Row, len(cols))
	for _, c := range cols {
		if v, ok := row[c]; ok {
			out[c] = v
			e.copies++
		}
	}
	return out, nil
}

// PrunedColumns 返回 all 中不在 visible 里的列名（字典序），用于审计报告。
func PrunedColumns(all, visible []string) []string {
	vis := make(map[string]struct{}, len(visible))
	for _, c := range visible {
		vis[c] = struct{}{}
	}
	var out []string
	for _, c := range all {
		if _, ok := vis[c]; !ok {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
