// Package wheel 单层时间轮：固定槽数、游标推进、超出本层量程的溢出判定。
package wheel

import "ontology/slot"

// Wheel 单层时间轮，槽数固定，游标循环推进。
type Wheel struct {
	slots []*slot.Slot
	pos   int
}

// New 创建 size 个槽的单层轮，游标初始为 0。
func New(size int) *Wheel {
	w := &Wheel{slots: make([]*slot.Slot, size)}
	for i := range w.slots {
		w.slots[i] = slot.New()
	}
	return w
}

// Advance 游标前进一格，返回是否绕回 0（即本层完成一圈，上一层应跟进）。
func (w *Wheel) Advance() (wrapped bool) {
	w.pos = (w.pos + 1) % len(w.slots)
	return w.pos == 0
}

// Overflow 判定以本层粒度计的 units 是否超出本层量程。
func (w *Wheel) Overflow(units int64) bool { return units >= int64(len(w.slots)) }

// Pos 返回当前游标。
func (w *Wheel) Pos() int { return w.pos }

// SetPos 设置游标（构造时对齐注入的起始时刻）。
func (w *Wheel) SetPos(p int) { w.pos = p }

// Size 返回槽数。
func (w *Wheel) Size() int { return len(w.slots) }

// Slot 返回第 i 个槽。
func (w *Wheel) Slot(i int) *slot.Slot { return w.slots[i] }

// Current 返回游标所在的槽。
func (w *Wheel) Current() *slot.Slot { return w.slots[w.pos] }

// Len 返回全轮元素总数。
func (w *Wheel) Len() int {
	n := 0
	for _, s := range w.slots {
		n += s.Len()
	}
	return n
}
