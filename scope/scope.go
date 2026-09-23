// Package scope 定义单个作用域：本层声明表与父作用域链接。依赖 name。
package scope

import (
	"errors"

	"ontology/name"
)

// ErrDuplicateDeclare 同层重复声明同一个名字，声明被拒绝。
var ErrDuplicateDeclare = errors.New("scope: duplicate declaration in same scope")

// Scope 是一层作用域。声明表只增不改，遮蔽靠链式查找实现，从不修改外层。
type Scope struct {
	parent *Scope
	depth  int
	decls  map[name.Name]name.Decl
}

// New 创建一层作用域；parent 为 nil 表示最外层（深度 0）。
func New(parent *Scope, depth int) *Scope {
	return &Scope{parent: parent, depth: depth, decls: make(map[name.Name]name.Decl)}
}

// Parent 返回父作用域，最外层返回 nil。
func (s *Scope) Parent() *Scope { return s.parent }

// Depth 返回本层深度（最外层为 0）。
func (s *Scope) Depth() int { return s.depth }

// Len 返回本层声明条数。
func (s *Scope) Len() int { return len(s.decls) }

// Declare 在本层加入一条声明；同层重名返回 ErrDuplicateDeclare 且不改变本层。
func (s *Scope) Declare(d name.Decl) error {
	if _, ok := s.decls[d.Name]; ok {
		return ErrDuplicateDeclare
	}
	s.decls[d.Name] = d
	return nil
}

// Lookup 用哈希定位本层声明，不遍历。
func (s *Scope) Lookup(n name.Name) (name.Decl, bool) {
	d, ok := s.decls[n]
	return d, ok
}

// Decls 返回本层声明表的副本，调用方修改不影响本层。
func (s *Scope) Decls() map[name.Name]name.Decl {
	out := make(map[name.Name]name.Decl, len(s.decls))
	for k, v := range s.decls {
		out[k] = v
	}
	return out
}
