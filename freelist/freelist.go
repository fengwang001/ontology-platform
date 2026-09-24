// Package freelist 维护空闲槽位的单向链表：头取、尾还，均为 O(1)，不扫描数组。
package freelist

import "ontology/slot"

// List 是空闲槽位链表。槽位记录本身存于表的槽位数组中，链表只记录头尾。
type List struct {
	head, tail int32
	n          int
	visited    int // 最近一次 Take 访问的槽位记录数（非导出，仅供包内测试断言 O(1)）
}

// New 返回空链表。
func New() *List { return &List{head: slot.Nil, tail: slot.Nil} }

// Len 返回链表中空闲槽位数。
func (l *List) Len() int { return l.n }

// Head 返回链头槽位号，供自检遍历；空链表返回 slot.Nil。
func (l *List) Head() int32 { return l.head }

// Take 取出链头槽位；ok 为 false 表示没有空闲槽位。
func (l *List) Take(slots []slot.Slot) (i int32, ok bool) {
	l.visited = 0
	if l.head == slot.Nil {
		return slot.Nil, false
	}
	l.visited++
	i = l.head
	l.head = slots[i].Next
	if l.head == slot.Nil {
		l.tail = slot.Nil
	}
	slots[i].Next = slot.Nil
	l.n--
	return i, true
}

// Put 把槽位还到链尾（尾插：均摊复用，推迟代号耗尽，见 DESIGN.md 第 3 节）。
func (l *List) Put(slots []slot.Slot, i int32) {
	slots[i].Next = slot.Nil
	if l.tail == slot.Nil {
		l.head = i
	} else {
		slots[l.tail].Next = i
	}
	l.tail = i
	l.n++
}
