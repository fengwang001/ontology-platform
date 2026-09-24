// Package freelist 维护空闲槽位的侵入式链表：取头、还尾，均为 O(1)。
package freelist

import "ontology/slot"

// List 是空闲槽位链表。取一个从头部取，还一个插到尾部（FIFO），
// 使复用轮转均摊、单槽位代号燃烧速率最低（见 DESIGN.md 推导三）。
type List struct {
	slots []slot.Slot // 与表共享的槽位数组
	next  []int32     // next[i] 是空闲链表中 i 的后继槽位号，-1 表示链尾
	head  int32
	tail  int32
	n     int
	// lastVisits 记录最近一次 Take 访问了多少个槽位记录。
	// 非导出计数器，仅供白盒测试核验 O(1)，不出现在公开接口。
	lastVisits int
}

// New 把 slots 的全部槽位按序号链成空闲链表。
func New(slots []slot.Slot) *List {
	next := make([]int32, len(slots))
	for i := range next {
		next[i] = int32(i) + 1
	}
	head, tail := int32(0), int32(len(next)-1)
	if len(next) == 0 {
		head, tail = -1, -1
	} else {
		next[len(next)-1] = -1
	}
	return &List{slots: slots, next: next, head: head, tail: tail, n: len(slots)}
}

// Take 从头部取一个空闲槽位，返回槽位记录指针与槽位号；空链表返回 false。
func (l *List) Take() (*slot.Slot, int, bool) {
	l.lastVisits = 0
	if l.head < 0 {
		return nil, 0, false
	}
	i := l.head
	l.lastVisits++ // 访问槽位记录 slots[i]，全程不扫描
	l.head = l.next[i]
	l.next[i] = -1
	if l.head < 0 {
		l.tail = -1
	}
	l.n--
	return &l.slots[i], int(i), true
}

// Give 把一个槽位还到链表尾部。
func (l *List) Give(i int) {
	if l.tail < 0 {
		l.head = int32(i)
	} else {
		l.next[l.tail] = int32(i)
	}
	l.tail = int32(i)
	l.next[i] = -1
	l.n++
}

// Len 返回空闲槽位数。
func (l *List) Len() int { return l.n }

// ForEach 按链表顺序遍历空闲槽位号，仅供自检使用。
func (l *List) ForEach(fn func(i int)) {
	for p := l.head; p >= 0; p = l.next[p] {
		fn(int(p))
	}
}
