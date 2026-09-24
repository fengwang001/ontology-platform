// Package table 是带代号的句柄表：槽位复用 + 代号校验，老句柄永不复活。
package table

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/freelist"
	"ontology/handle"
	"ontology/slot"
)

var (
	ErrBadCap        = errors.New("table: 容量必须为正且不超过 handle.MaxSlot")
	ErrTableFull     = errors.New("table: 表已满，无可用槽位")
	ErrZeroHandle    = errors.New("table: 零值句柄")
	ErrWrongTable    = errors.New("table: 句柄不属于本表")
	ErrStale         = errors.New("table: 句柄已失效")
	ErrSlotExhausted = errors.New("table: 槽位代号已耗尽退役")
	errNoTableID     = errors.New("table: 表号空间耗尽")
)

var tableSeq atomic.Uint32 // 表号从 1 起分配，保证任何句柄的 tableID 字段非零

// Table 是固定容量的句柄表，可并发使用。
type Table struct {
	mu    sync.RWMutex
	id    uint32
	slots []slot.Slot
	free  *freelist.List
	alive int
}

// New 建表；capacity 必须为正且不超过 handle.MaxSlot。
func New(capacity int) (*Table, error) {
	if capacity <= 0 || capacity > handle.MaxSlot {
		return nil, ErrBadCap
	}
	id := tableSeq.Add(1)
	if id == 0 || id > handle.MaxTable {
		return nil, errNoTableID
	}
	t := &Table{id: id, slots: make([]slot.Slot, capacity), free: freelist.New()}
	for i := range t.slots {
		t.slots[i].Gen = 1 // 代号从 1 起步，「第 0 代」永不发出（DESIGN.md 第 2 节）
		t.free.Put(t.slots, int32(i))
	}
	return t, nil
}

// Insert 占用一个空闲槽位，返回其句柄；表满返回 ErrTableFull。
func (t *Table) Insert(v any) (handle.Handle, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	i, ok := t.free.Take(t.slots)
	if !ok {
		return 0, ErrTableFull
	}
	s := &t.slots[i]
	s.Value, s.State = v, slot.InUse
	t.alive++
	return handle.Make(t.id, s.Gen, uint32(i)), nil
}

// Get 取出句柄指向的值；句柄无效时返回可判定的错误。
func (t *Table) Get(h handle.Handle) (any, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, err := t.lookup(h)
	if err != nil {
		return nil, err
	}
	return s.Value, nil
}

// Remove 释放句柄指向的槽位；代号递进使老句柄失效，触顶则槽位退役。
func (t *Table) Remove(h handle.Handle) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, err := t.lookup(h)
	if err != nil {
		return err
	}
	s.Value = nil
	t.alive--
	if s.Gen == handle.MaxGen {
		s.State = slot.Exhausted // 代号不回绕，触顶退役（DESIGN.md 第 1 节）
		return nil
	}
	s.Gen++
	s.State = slot.Free
	t.free.Put(t.slots, int32(h.Slot()))
	return nil
}

func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.alive
}

func (t *Table) Cap() int { return len(t.slots) }

// lookup 校验句柄并定位槽位；被拒时不改变任何状态。
func (t *Table) lookup(h handle.Handle) (*slot.Slot, error) {
	switch {
	case h.IsZero():
		return nil, ErrZeroHandle
	case h.TableID() != t.id:
		return nil, ErrWrongTable
	case h.Slot() >= uint32(len(t.slots)):
		return nil, ErrStale
	}
	s := &t.slots[h.Slot()]
	if s.Gen != h.Gen() || s.State == slot.Free {
		return nil, ErrStale
	}
	if s.State == slot.Exhausted {
		return nil, ErrSlotExhausted
	}
	return s, nil
}

type SlotView struct {
	State slot.State
	Gen   uint32
}

func (t *Table) Inspect() ([]SlotView, []int32) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	views := make([]SlotView, len(t.slots))
	for i, s := range t.slots {
		views[i] = SlotView{State: s.State, Gen: s.Gen}
	}
	free := make([]int32, 0, t.free.Len())
	for i := t.free.Head(); i != slot.Nil; i = t.slots[i].Next {
		free = append(free, i)
	}
	return views, free
}

// pushGenToMax 仅供测试：把在用槽位 idx 的代号快进到上限，返回占用者的新句柄。
func (t *Table) pushGenToMax(idx int) handle.Handle {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.slots[idx].Gen = handle.MaxGen
	return handle.Make(t.id, handle.MaxGen, uint32(idx))
}
