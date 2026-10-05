// Package mapping 维护字段树、字段总数上限与映射版本。
//
// Mapping 本身非并发安全，由调用方（ingest.Engine）串行化访问。
package mapping

import (
	"errors"
	"fmt"
)

// Type 是字段类型。
type Type int

const (
	Long Type = iota
	Double
	Keyword
	Bool
	Object
)

func (t Type) String() string {
	switch t {
	case Long:
		return "long"
	case Double:
		return "double"
	case Keyword:
		return "keyword"
	case Bool:
		return "bool"
	case Object:
		return "object"
	}
	return "unknown"
}

// Valid 报告 t 是否为合法字段类型。
func (t Type) Valid() bool { return t >= Long && t <= Object }

// Dynamic 是动态映射模式。
type Dynamic int

const (
	True Dynamic = iota
	False
	Strict
)

// Valid 报告 d 是否为合法动态模式。
func (d Dynamic) Valid() bool { return d >= True && d <= Strict }

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrTypeConflict    = errors.New("type conflict")
	ErrStrict          = errors.New("strict mode rejection")
	ErrFieldLimit      = errors.New("field limit exceeded")
	ErrDocNotFound     = errors.New("document not found")
)

// PathError 把哨兵错误与字段路径绑定，errors.Is 可区分类别。
type PathError struct {
	Path   string
	Err    error
	Detail string
}

func (e *PathError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%v: %s: %s", e.Err, e.Path, e.Detail)
	}
	return fmt.Sprintf("%v: %s", e.Err, e.Path)
}

func (e *PathError) Unwrap() error { return e.Err }

// Node 是字段树节点的 opaque 句柄。
type Node struct {
	typ      Type
	children map[string]*Node
}

// Mapping 是字段树加版本与计数。
type Mapping struct {
	root    *Node
	count   int
	mv      int
	fmax    int
	touched int
}

// New 创建空映射，fmax 须在 [1, 100000]。
func New(fmax int) (*Mapping, error) {
	if fmax < 1 || fmax > 100000 {
		return nil, fmt.Errorf("%w: fmax %d out of [1, 100000]", ErrInvalidArgument, fmax)
	}
	return &Mapping{root: &Node{typ: Object}, fmax: fmax}, nil
}

// Root 返回根节点（object 类型，不占字段名额）。
func (m *Mapping) Root() *Node { return m.root }

// TypeOf 返回节点类型。
func (m *Mapping) TypeOf(n *Node) Type { return n.typ }

// Child 返回 key 的子节点（无则 nil），计一次触碰。
func (m *Mapping) Child(n *Node, key string) *Node {
	m.touched++
	return n.children[key]
}

// ChildOrAdd 返回 key 的子节点；不存在则以 typ 新建，计一次触碰。
// 超过 Fmax 时返回 ErrFieldLimit 且不改任何状态。
func (m *Mapping) ChildOrAdd(parent *Node, key string, typ Type) (n *Node, created bool, err error) {
	m.touched++
	if c := parent.children[key]; c != nil {
		return c, false, nil
	}
	if m.count+1 > m.fmax {
		return nil, false, ErrFieldLimit
	}
	n = &Node{typ: typ}
	if parent.children == nil {
		parent.children = make(map[string]*Node)
	}
	parent.children[key] = n
	m.count++
	return n, true, nil
}

// AddChild 在 parent 下新建子节点（PutMapping 用，调用前已预检 Fmax）。
func (m *Mapping) AddChild(parent *Node, key string, typ Type) *Node {
	m.touched++
	n := &Node{typ: typ}
	if parent.children == nil {
		parent.children = make(map[string]*Node)
	}
	parent.children[key] = n
	m.count++
	return n
}

// RemoveChild 删除刚由 ChildOrAdd/AddChild 创建的子节点（回滚用）。
func (m *Mapping) RemoveChild(parent *Node, key string) {
	delete(parent.children, key)
	m.count--
}

// Count 返回字段总数（object 字段也占名额）。
func (m *Mapping) Count() int { return m.count }

// Fmax 返回字段总数上限。
func (m *Mapping) Fmax() int { return m.fmax }

// MV 返回当前映射版本。
func (m *Mapping) MV() int { return m.mv }

// BumpMV 把映射版本加一（一次被接受的操作只调一次）。
func (m *Mapping) BumpMV() { m.mv++ }

// Touched 返回累计触碰的映射节点数。
func (m *Mapping) Touched() int { return m.touched }

// ResetTouched 清零触碰计数器。
func (m *Mapping) ResetTouched() { m.touched = 0 }

// Fields 导出全部字段路径到类型的快照（不含根）。
func (m *Mapping) Fields() map[string]Type {
	out := make(map[string]Type, m.count)
	var walk func(n *Node, prefix string)
	walk = func(n *Node, prefix string) {
		for k, c := range n.children {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = c.typ
			walk(c, p)
		}
	}
	walk(m.root, "")
	return out
}
