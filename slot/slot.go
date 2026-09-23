// Package slot 实现固定槽数(2 的幂)的槽位环;槽内为保序双向链表,O(1) 摘除。
package slot

// Element 是槽内双向链表节点。
type Element struct {
	Value any
	prev  *Element
	next  *Element
	slot  int
	ring  *Ring
}

// Next 返回后继。
func (e *Element) Next() *Element { return e.next }

// Ring 是槽位环。
type Ring struct {
	mask  int
	head  []*Element
	tail  []*Element
	count []int
}

// New 创建槽数为 slots(必须是 2 的幂)的环。
func New(slots int) *Ring {
	if slots&(slots-1) != 0 || slots <= 0 {
		panic("slot: slots must be power of two")
	}
	return &Ring{mask: slots - 1, head: make([]*Element, slots), tail: make([]*Element, slots), count: make([]int, slots)}
}

// Slots 返回槽数。
func (r *Ring) Slots() int { return len(r.head) }

// Idx 把绝对槽序号折回环内下标。
func (r *Ring) Idx(abs int64) int { return int(abs) & r.mask }

// Len 返回某槽元素数。
func (r *Ring) Len(idx int) int { return r.count[idx] }

// AddOrdered 按 less 升序把 v 插入槽 idx(链须已有序),返回元素句柄。
func (r *Ring) AddOrdered(idx int, v any, less func(a, b any) bool) *Element {
	e := &Element{Value: v, slot: idx, ring: r}
	at := r.head[idx]
	for at != nil && !less(v, at.Value) {
		at = at.next
	}
	if at == nil {
		if r.tail[idx] != nil {
			e.prev = r.tail[idx]
			r.tail[idx].next = e
		} else {
			r.head[idx] = e
		}
		r.tail[idx] = e
	} else {
		e.prev, e.next = at.prev, at
		if at.prev != nil {
			at.prev.next = e
		} else {
			r.head[idx] = e
		}
		at.prev = e
	}
	r.count[idx]++
	return e
}

// Remove 从环中摘除 e,O(1)。重复摘除无效果。
func (r *Ring) Remove(e *Element) {
	if e == nil || e.ring != r {
		return
	}
	i := e.slot
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		r.head[i] = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		r.tail[i] = e.prev
	}
	e.ring, e.prev, e.next = nil, nil, nil
	r.count[i]--
}

// Chain 是摘下的一条链表。
type Chain struct {
	Head, Tail *Element
	N          int
}

// Detach 摘下槽 idx 的整条链并清空该槽。
func (r *Ring) Detach(idx int) Chain {
	c := Chain{Head: r.head[idx], Tail: r.tail[idx], N: r.count[idx]}
	r.head[idx], r.tail[idx], r.count[idx] = nil, nil, 0
	return c
}

// MergeOrdered 把已有序链 c 按 less 归并进已有序槽 idx。
func (r *Ring) MergeOrdered(idx int, c Chain, less func(a, b any) bool) {
	if c.N == 0 {
		return
	}
	a, b := r.head[idx], c.Head
	var head, tail *Element
	link := func(e *Element) {
		e.ring, e.slot, e.prev, e.next = r, idx, tail, nil
		if tail != nil {
			tail.next = e
		} else {
			head = e
		}
		tail = e
	}
	for a != nil || b != nil {
		if b == nil || (a != nil && !less(b.Value, a.Value)) {
			next := a.next
			link(a)
			a = next
		} else {
			next := b.next
			link(b)
			b = next
		}
	}
	r.head[idx], r.tail[idx] = head, tail
	r.count[idx] += c.N
}
