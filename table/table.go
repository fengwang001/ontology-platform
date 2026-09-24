// Package table 实现带代号的句柄表：槽位复用 + 代号校验。
package table

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/freelist"
	"ontology/handle"
	"ontology/slot"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrInvalidCapacity = errors.New("table: capacity must be in [1, MaxIndex+1]")
	ErrFull            = errors.New("table: no free slot")
	ErrZeroHandle      = errors.New("table: zero handle")
	ErrForeignHandle   = errors.New("table: handle belongs to another table")
	ErrStaleHandle     = errors.New("table: stale handle")
)

// tagCounter 分配表标签，从 1 开始，0 预留给零值句柄（DESIGN.md 推导二）。
var tagCounter atomic.Uint32

// Table 是句柄表本体，并发安全。
type Table struct {
	mu    sync.Mutex
	slots []slot.Slot
	free  *freelist.List
	tag   uint16
	live  int // 在用槽位数
	dead  int // 代号耗尽槽位数
}

// New 创建容量为 capacity 的句柄表；容量非法时返回 ErrInvalidCapacity。
func New(capacity int) (*Table, error) {
	if capacity <= 0 || capacity > handle.MaxIndex+1 {
		return nil, ErrInvalidCapacity
	}
	slots := make([]slot.Slot, capacity)
	for i := range slots {
		slots[i].Gen = 1 // 代号从 1 开始，0 永不发出
	}
	tag := uint16(tagCounter.Add(1)%65535 + 1) // 1..65535，永不取 0
	return &Table{slots: slots, free: freelist.New(slots), tag: tag}, nil
}

// Insert 存入一个值，返回其句柄；表满返回 ErrFull 且不留半插入槽位。
func (t *Table) Insert(v any) (handle.Handle, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, idx, ok := t.free.Take()
	if !ok {
		return handle.Zero, ErrFull
	}
	s.Value, s.InUse = v, true
	t.live++
	return handle.New(t.tag, uint32(idx), s.Gen), nil
}

// Get 按句柄取出值；句柄非法时返回可判定的具体错误。
func (t *Table) Get(h handle.Handle) (any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, err := t.lookup(h); err != nil {
		return nil, err
	} else {
		return s.Value, nil
	}
}

// Remove 使句柄失效并回收槽位；代号达上限的槽位退役不再复用。
func (t *Table) Remove(h handle.Handle) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, err := t.lookup(h)
	if err != nil {
		return err
	}
	s.Value, s.InUse = nil, false
	t.live--
	if s.Gen >= handle.MaxGen {
		s.Exhausted = true // 代号不回绕：耗尽即退役（DESIGN.md 推导一）
		t.dead++
	} else {
		s.Gen++
		t.free.Give(int(h.Index()))
	}
	return nil
}

// lookup 校验句柄并返回槽位；失败时不改变任何状态。
func (t *Table) lookup(h handle.Handle) (*slot.Slot, error) {
	switch {
	case h.IsZero():
		return nil, ErrZeroHandle
	case h.Tag() != t.tag:
		return nil, ErrForeignHandle
	case h.Index() >= uint32(len(t.slots)):
		return nil, ErrStaleHandle
	}
	s := &t.slots[h.Index()]
	if !s.InUse || s.Gen != h.Gen() {
		return nil, ErrStaleHandle
	}
	return s, nil
}

// Len 返回当前校验通过的句柄个数。
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.live
}

// Cap 返回表容量。
func (t *Table) Cap() int { return len(t.slots) }

// SlotInfo 是单个槽位的自检视图。
type SlotInfo struct {
	Index     int
	Gen       uint32
	Handle    handle.Handle // 仅在 InUse 时有效
	InUse     bool
	Exhausted bool
}

// Slots 返回全部槽位的快照，供 audit 包与演示程序自检。
func (t *Table) Slots() []SlotInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SlotInfo, len(t.slots))
	for i, s := range t.slots {
		out[i] = SlotInfo{Index: i, Gen: s.Gen, InUse: s.InUse, Exhausted: s.Exhausted}
		if s.InUse {
			out[i].Handle = handle.New(t.tag, uint32(i), s.Gen)
		}
	}
	return out
}

// FreeIndices 按链表顺序返回空闲槽位号快照。
func (t *Table) FreeIndices() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []int{}
	t.free.ForEach(func(i int) { out = append(out, i) })
	return out
}
