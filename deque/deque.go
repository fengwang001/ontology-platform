// Package deque 提供双端队列：两端入出、按下标定位队首、长度。
package deque

// Item 是队列元素：序列下标与对应的值。
type Item struct {
	Index int
	Value int
}

// Deque 以切片加队首偏移实现，弹出均摊 O(1)。
type Deque struct {
	items []Item
	head  int
}

// Len 返回队列长度。
func (d *Deque) Len() int { return len(d.items) - d.head }

// PushBack 在队尾入队一个元素。
func (d *Deque) PushBack(it Item) { d.items = append(d.items, it) }

// PopBack 弹出并返回队尾元素。
func (d *Deque) PopBack() Item {
	it := d.items[len(d.items)-1]
	d.items = d.items[:len(d.items)-1]
	return it
}

// PopFront 弹出并返回队首元素。
func (d *Deque) PopFront() Item {
	it := d.items[d.head]
	d.head++
	if d.head == len(d.items) { // 排空后回收底层数组
		d.items = d.items[:0]
		d.head = 0
	}
	return it
}

// Front 返回队首元素。
func (d *Deque) Front() Item { return d.items[d.head] }

// Back 返回队尾元素。
func (d *Deque) Back() Item { return d.items[len(d.items)-1] }

// FrontIndex 返回队首元素的序列下标。
func (d *Deque) FrontIndex() int { return d.items[d.head].Index }

// At 返回从队首起第 k 个元素（0 为队首）。
func (d *Deque) At(k int) Item { return d.items[d.head+k] }
