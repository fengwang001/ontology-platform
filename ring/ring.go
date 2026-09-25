// Package ring 维护一个闭合的逻辑环：节点、后继指针、增删节点。
// 不依赖其他包。
package ring

import (
	"errors"
	"sync/atomic"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyID  = errors.New("ring: empty node id")
	ErrDupID    = errors.New("ring: duplicate node id")
	ErrNotFound = errors.New("ring: node not found")
	ErrEmpty    = errors.New("ring: no nodes")
)

type node struct {
	id   string
	next *node
}

// Ring 是闭合的单向链表环。head/tail 指向链表的入口与末尾，
// tail.next 恒等于 head（空环时均为 nil）。
type Ring struct {
	nodes map[string]*node
	head  *node
	tail  *node
	n     int
	// succChecks 记录最近一次后继查询检查的节点条目个数。
	// 非导出，不出现在任何公开接口里。
	succChecks atomic.Int64
}

func New() *Ring { return &Ring{nodes: map[string]*node{}} }

// Add 把 id 追加到环尾。空 ID、重复 ID 整体失败且不改任何状态。
func (r *Ring) Add(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := r.nodes[id]; ok {
		return ErrDupID
	}
	nd := &node{id: id}
	if r.n == 0 {
		nd.next = nd
		r.head, r.tail = nd, nd
	} else {
		nd.next = r.head
		r.tail.next = nd
		r.tail = nd
	}
	r.nodes[id] = nd
	r.n++
	return nil
}

// Remove 摘除 id。删除不存在的节点整体失败且不改任何状态。
func (r *Ring) Remove(id string) error {
	nd, ok := r.nodes[id]
	if !ok {
		return ErrNotFound
	}
	if r.n == 1 {
		r.head, r.tail = nil, nil
	} else {
		pred := r.tail
		for pred.next != nd {
			pred = pred.next
		}
		pred.next = nd.next
		if nd == r.head {
			r.head = nd.next
		}
		if nd == r.tail {
			r.tail = pred
		}
	}
	delete(r.nodes, id)
	r.n--
	return nil
}

// Successor 经后继指针直接定位，只检查 1 个节点条目，不整表扫描。
func (r *Ring) Successor(id string) (string, error) {
	nd, ok := r.nodes[id]
	if !ok {
		return "", ErrNotFound
	}
	r.succChecks.Store(1)
	return nd.next.id, nil
}

// Size 返回节点数。
func (r *Ring) Size() int { return r.n }

// Order 按环序（从 head 起）返回全部 ID。
func (r *Ring) Order() []string {
	out := make([]string, 0, r.n)
	for cur := r.head; cur != nil && len(out) < r.n; cur = cur.next {
		out = append(out, cur.id)
	}
	return out
}

// Max 朴素遍历取最大 ID，作为选举正确性的参照。
func (r *Ring) Max() (string, error) {
	if r.n == 0 {
		return "", ErrEmpty
	}
	max := r.head.id
	for cur := r.head.next; cur != r.head; cur = cur.next {
		if cur.id > max {
			max = cur.id
		}
	}
	return max, nil
}
