// Package resolve 从引用点沿作用域链向外解析名字。依赖 scope。
package resolve

import (
	"errors"
	"sync/atomic"

	"ontology/name"
	"ontology/scope"
)

var (
	// ErrUndefined 整条作用域链上不存在该名字。
	ErrUndefined = errors.New("resolve: undefined name")
	// ErrUseBeforeDeclare 名字已声明但不允许前向引用，引用点在声明之前。
	ErrUseBeforeDeclare = errors.New("resolve: use before declaration")
)

// Ref 是一个引用点：所在作用域、名字、源码位置序号。
type Ref struct {
	Scope *scope.Scope
	Name  name.Name
	Pos   int
}

// Result 是一次成功的解析：命中的声明与它所在的作用域深度。
type Result struct {
	Decl  name.Decl
	Depth int
}

// Resolver 执行名称解析。looked 统计最近一次解析查看的声明条目数，
// 每访问一层作用域的声明表计 1（哈希定位），是非导出字段。
type Resolver struct {
	looked atomic.Int64
}

// NewResolver 构造一个解析器。
func NewResolver() *Resolver { return &Resolver{} }

// Looked 返回最近一次解析查看的声明条目数（只读访问器）。
func (r *Resolver) Looked() int64 { return r.looked.Load() }

// Resolve 从引用点沿父链向外查找：本层命中即裁决，不再向外。
func (r *Resolver) Resolve(ref Ref) (Result, error) {
	r.looked.Store(0)
	for s := ref.Scope; s != nil; s = s.Parent() {
		r.looked.Add(1)
		d, ok := s.Lookup(ref.Name)
		if !ok {
			continue
		}
		if d.Kind == name.KindForward || ref.Pos >= d.Pos {
			return Result{Decl: d, Depth: s.Depth()}, nil
		}
		return Result{}, ErrUseBeforeDeclare
	}
	return Result{}, ErrUndefined
}
