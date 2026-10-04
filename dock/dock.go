package dock

import (
	"bytes"
	"container/heap"

	"ontology/appt"
)

var (
	ErrInvalid   = appt.ErrInvalid
	ErrClock     = appt.ErrClock
	ErrNotFound  = appt.ErrNotFound
	ErrState     = appt.ErrState
	ErrDuplicate = appt.ErrDuplicate
)

type Kind = appt.Kind

const (
	Dry    = appt.Dry
	Reefer = appt.Reefer
)

type Docks struct {
	lastNow int64
	all     map[string]Kind
	free    [2]*idHeap
}

func New(_ appt.Config) *Docks {
	d := &Docks{all: make(map[string]Kind)}
	d.free[appt.Dry] = &idHeap{}
	d.free[appt.Reefer] = &idHeap{}
	return d
}

func (d *Docks) AddDock(id []byte, kind Kind, now int64) error {
	if !appt.ValidID(id) || (kind != Dry && kind != Reefer) {
		return ErrInvalid
	}
	if now < d.lastNow {
		return ErrClock
	}
	key := string(id)
	if _, ok := d.all[key]; ok {
		return ErrDuplicate
	}
	d.all[key] = kind
	heap.Push(d.free[kind], cloneBytes(id))
	d.lastNow = now
	return nil
}

// HasFree 报告指定类型是否存在空闲月台。
func (d *Docks) HasFree(kind Kind) bool {
	return d.free[kind].Len() > 0
}

// FreeDry 与 FreeReefer 暴露空闲状态，供 yard 判定借用条件。
func (d *Docks) FreeDry() bool    { return d.free[Dry].Len() > 0 }
func (d *Docks) FreeReefer() bool { return d.free[Reefer].Len() > 0 }

// Take 取走一个月台：常温优先常温位；borrowReefer 为真且无常温位时借冷藏位。
// 冷藏车只取冷藏位。
func (d *Docks) Take(kind Kind, borrowReefer bool) ([]byte, bool) {
	if kind == Reefer {
		if d.free[Reefer].Len() == 0 {
			return nil, false
		}
		return heap.Pop(d.free[Reefer]).([]byte), true
	}
	if d.free[Dry].Len() > 0 {
		return heap.Pop(d.free[Dry]).([]byte), true
	}
	if borrowReefer && d.free[Reefer].Len() > 0 {
		return heap.Pop(d.free[Reefer]).([]byte), true
	}
	return nil, false
}

// Release 归还月台。调用者须保证月台此前确实被占用。
func (d *Docks) Release(id []byte) {
	kind, ok := d.all[string(id)]
	if !ok {
		return
	}
	heap.Push(d.free[kind], cloneBytes(id))
}

func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

type idHeap [][]byte

func (h idHeap) Len() int { return len(h) }
func (h idHeap) Less(i, j int) bool {
	return bytes.Compare(h[i], h[j]) < 0
}
func (h idHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x any)   { *h = append(*h, x.([]byte)) }
func (h *idHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
