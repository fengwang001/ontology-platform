// Package lru 维护 MRU→LRU 顺序的双向链表，不依赖其他包。
package lru

// Element 是链表节点，Value 为驻留字符串。
type Element struct {
	Value      string
	prev, next *Element
	list       *List
}

// List 头端为 MRU，尾端为 LRU。
type List struct {
	head, tail *Element
	n          int
}

// Len 返回链表中的元素个数。
func (l *List) Len() int { return l.n }

// Front 返回 MRU 端元素，空表返回 nil。
func (l *List) Front() *Element { return l.head }

// Back 返回 LRU 端元素，空表返回 nil。
func (l *List) Back() *Element { return l.tail }

// Next 返回 e 向 MRU 方向的下一个元素。
func (l *List) Next(e *Element) *Element {
	if e == nil || e.list != l {
		return nil
	}
	return e.next
}

// Prev 返回 e 向 LRU 方向的下一个元素。
func (l *List) Prev(e *Element) *Element {
	if e == nil || e.list != l {
		return nil
	}
	return e.prev
}

// PushFront 把值插入 MRU 端并返回新元素。
func (l *List) PushFront(v string) *Element {
	e := &Element{Value: v, list: l}
	e.next = l.head
	if l.head != nil {
		l.head.prev = e
	} else {
		l.tail = e
	}
	l.head = e
	l.n++
	return e
}

// MoveToFront 把已存在的元素移到 MRU 端。
func (l *List) MoveToFront(e *Element) {
	if e == nil || e.list != l || l.head == e {
		return
	}
	l.detach(e)
	e.prev = nil
	e.next = l.head
	l.head.prev = e
	l.head = e
}

// Remove 从链表中删除元素。
func (l *List) Remove(e *Element) {
	if e == nil || e.list != l {
		return
	}
	l.detach(e)
	e.list = nil
	e.next = nil
	e.prev = nil
	l.n--
}

func (l *List) detach(e *Element) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		l.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		l.tail = e.prev
	}
}
