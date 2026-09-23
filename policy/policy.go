// Package policy 描述每个头部的语义策略：
// 单值/列表型、重复时取首个/取末个/合并/报错、值大小写敏感性。
package policy

import "ontology/token"

// Merge 是重复头部的单值归并策略。
type Merge int

const (
	// First 取第一次出现的值。
	First Merge = iota
	// Last 取最后一次出现的值。
	Last
	// Join 把全部值按列表语义合并（", " 连接）。
	Join
	// Error 重复出现即报错。
	Error
)

// Policy 是一个头部的语义策略。
type Policy struct {
	// List 为 true 表示该头部是列表型（逗号分隔）。
	List bool
	// Merge 决定重复出现时单值访问的行为。
	Merge Merge
	// CaseSensitiveValue 为 true 表示值大小写敏感（仅作元数据，
	// 供上层比较语义参考；本库回写不做大小写改写）。
	CaseSensitiveValue bool
}

// Table 按规范化后的头部名登记策略。
type Table struct {
	m        map[string]Policy
	fallback Policy
}

// New 创建空表，未登记的名字使用 fallback。
func New(fallback Policy) *Table {
	return &Table{m: make(map[string]Policy), fallback: fallback}
}

// Register 登记一个名字的策略（名字会被规范化）。
func (t *Table) Register(name string, p Policy) {
	t.m[token.Canonical(name)] = p
}

// Lookup 查询一个名字的策略，未登记时返回 fallback。
func (t *Table) Lookup(name string) Policy {
	if p, ok := t.m[token.Canonical(name)]; ok {
		return p
	}
	return t.fallback
}

// Default 返回内置策略表：未登记名字默认为"单值、重复取首个"。
func Default() *Table {
	t := New(Policy{List: false, Merge: First})
	t.Register("Content-Length", Policy{Merge: Error})
	t.Register("Host", Policy{Merge: Error})
	t.Register("Set-Cookie", Policy{Merge: Error, CaseSensitiveValue: true})
	t.Register("Accept", Policy{List: true, Merge: Join})
	t.Register("Accept-Encoding", Policy{List: true, Merge: Join})
	t.Register("Cache-Control", Policy{List: true, Merge: Join})
	t.Register("If-Match", Policy{List: true, Merge: Join})
	t.Register("Vary", Policy{List: true, Merge: Join})
	t.Register("X-Multi", Policy{Merge: Join})
	t.Register("X-Last", Policy{Merge: Last})
	return t
}
