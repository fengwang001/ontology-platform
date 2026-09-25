// Package api 是投影下推与列裁剪的对外门面：定义源列、设置投影、逐行应用变更、
// 读取物化视图。它只依赖 prune（进而依赖 col），依赖方向单向。
package api

import (
	"errors"
	"sync"

	"ontology/col"
	"ontology/prune"
)

// 四类可判定、互不相同的哨兵错误。
var (
	ErrInvalidSchema   = col.ErrInvalidSchema
	ErrUnknownRef      = prune.ErrUnknownRef
	ErrDuplicateOutput = prune.ErrDuplicateOutput
	ErrInvalidRow      = col.ErrInvalidRow
)

// Output 是一个对外的投影输出列声明。
type Output struct {
	Out  string
	Refs []string
}

// Engine 是进程内的物化视图引擎，状态全部在内存中。
type Engine struct {
	mu     sync.RWMutex
	schema *col.Schema
	proj   *prune.Projection
	view   [][]int
}

// New 用源列模式创建引擎；模式为空或列名重复时整体失败。
func New(cols []string) (*Engine, error) {
	s, err := col.New(cols)
	if err != nil {
		return nil, err
	}
	return &Engine{schema: s}, nil
}

// SetProjection 整体替换投影。先在锁外完成全部校验与构造，
// 成功后才在写锁内替换；任何失败都不改变现有投影与视图。
func (e *Engine) SetProjection(outs []Output) error {
	defs := make([]prune.Def, len(outs))
	for i, o := range outs {
		defs[i] = prune.Def{Out: o.Out, Refs: append([]string(nil), o.Refs...)}
	}
	p, err := prune.New(e.schema, defs)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proj = p
	e.view = nil // 投影定义变了，旧视图语义上不再属于新投影
	return nil
}

// Apply 校验并应用一个完整变更行：缺列或含未知列即拒绝，不追加任何输出。
func (e *Engine) Apply(row map[string]int) error {
	if err := e.schema.CheckRow(row); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.proj == nil {
		// 未设投影时每行投影为空行；仍校验行的合法性。
		e.view = append(e.view, []int{})
		return nil
	}
	e.view = append(e.view, e.proj.Project(row))
	return nil
}

// View 返回已产出投影的深拷贝，按喂入顺序排列；可安全并发调用。
func (e *Engine) View() [][]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([][]int, len(e.view))
	for i, r := range e.view {
		out[i] = append([]int(nil), r...)
	}
	return out
}

// OutputNames 按投影定义顺序返回输出列名副本；可安全并发调用。
func (e *Engine) OutputNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.proj == nil {
		return nil
	}
	return e.proj.Names()
}

// SelfCheck 用内置投影与行序列核验四条不变量，全部成立返回 nil。
// 它只构造局部状态，不改动接收者，可安全并发调用。
func (e *Engine) SelfCheck() error {
	s, err := col.New([]string{"a", "b", "c", "d", "e"})
	if err != nil {
		return err
	}
	p, err := prune.New(s, []prune.Def{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	})
	if err != nil {
		return err
	}
	want := []string{"a", "b", "c", "d"}
	if got := p.Retained(); !eq(got, want) {
		return errors.New("api: self-check pruning set mismatch")
	}
	if names := p.Names(); !eq(names, []string{"x", "sum", "y"}) {
		return errors.New("api: self-check output names mismatch")
	}
	rows := []map[string]int{
		{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5},
		{"a": 6, "b": 7, "c": 8, "d": 9, "e": 10},
		{"a": 11, "b": 12, "c": 13, "d": 14, "e": 15},
	}
	exp := [][]int{{1, 5, 4}, {6, 15, 9}, {11, 25, 14}}
	for i, row := range rows {
		if got := p.Project(row); !eq(got, exp[i]) {
			return errors.New("api: self-check projection value mismatch")
		}
	}
	return nil
}

func eq[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
