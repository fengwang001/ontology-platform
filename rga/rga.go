// Package rga 是基于 RGA 的协同文本序列 CRDT 的最底层：元素树与墓碑。
// 它不依赖其他任何包。
package rga

import (
	"errors"
	"strings"
)

// ID 是元素全局唯一标识：(Lamport 时间戳, 副本名)。零值 Empty 表示 ∅。
type ID struct {
	Lamport uint64
	Replica string
}

// Empty 是特殊前驱 ∅：插到文档最前。
var Empty = ID{}

// 可判定的哨兵错误。
var (
	ErrInvalidID      = errors.New("rga: invalid id: empty replica")
	ErrDuplicateID    = errors.New("rga: duplicate element id")
	ErrPrevNotFound   = errors.New("rga: prev element not found")
	ErrIDNotFound     = errors.New("rga: element id not found")
	ErrAlreadyDeleted = errors.New("rga: element already deleted")
)

// Element 是一个可见字符元素。
type Element struct {
	ID ID
	Ch rune
}

type node struct {
	el   Element
	dead bool
	kids []*node // 始终按 greaterID 降序保持
}

// Tree 是元素树：根为 prev=∅ 的元素；墓碑不摘除。
type Tree struct {
	roots []*node
	byID  map[ID]*node
}

func NewTree() *Tree { return &Tree{byID: map[ID]*node{}} }

// greaterID 定义同一 prev 下并发子元素的全序：
// lamport 降序（大的更靠近 prev），相同时 replica 降序。
func greaterID(a, b ID) bool {
	if a.Lamport != b.Lamport {
		return a.Lamport > b.Lamport
	}
	return a.Replica > b.Replica
}

func (t *Tree) Has(id ID) bool { _, ok := t.byID[id]; return ok }

// insertSorted 把 n 按降序插进 sibs：排在第一个严格小于它的兄弟之前。
func insertSorted(sibs *[]*node, n *node) {
	s := *sibs
	i := 0
	for i < len(s) && !greaterID(n.el.ID, s[i].el.ID) {
		i++
	}
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = n
	*sibs = s
}

// Add 在 prev（Empty 表示最前）之后插入字符 ch，标识为 id。
// 所有校验先于任何状态变更：被拒绝时树与 map 均不变。
func (t *Tree) Add(prev, id ID, ch rune) error {
	if id.Replica == "" {
		return ErrInvalidID
	}
	if _, dup := t.byID[id]; dup {
		return ErrDuplicateID
	}
	var parent *node
	if prev != Empty {
		p, ok := t.byID[prev]
		if !ok {
			return ErrPrevNotFound
		}
		parent = p
	}
	n := &node{el: Element{ID: id, Ch: ch}}
	t.byID[id] = n
	if parent == nil {
		insertSorted(&t.roots, n)
	} else {
		insertSorted(&parent.kids, n)
	}
	return nil
}

// Delete 把 id 标为墓碑：不物理移除，子孙仍可被遍历与定位。
func (t *Tree) Delete(id ID) error {
	n, ok := t.byID[id]
	if !ok {
		return ErrIDNotFound
	}
	if n.dead {
		return ErrAlreadyDeleted
	}
	n.dead = true
	return nil
}

// Visible 按「元素后接其子元素」DFS 产出可见文本；
// 跳过墓碑元素的字符，但仍递归它的子元素。
func (t *Tree) Visible() string {
	var b strings.Builder
	var walk func(*node)
	walk = func(n *node) {
		if !n.dead {
			b.WriteRune(n.el.Ch)
		}
		for _, c := range n.kids {
			walk(c)
		}
	}
	for _, r := range t.roots {
		walk(r)
	}
	return b.String()
}
