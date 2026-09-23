package table

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/freelist"
	"ontology/handle"
	"ontology/slot"
)

var (
	ErrInvalidCapacity     = errors.New("table: capacity must be positive")
	ErrTableFull           = errors.New("table: table is full")
	ErrZeroHandle          = errors.New("table: zero handle is invalid")
	ErrForeignTable        = errors.New("table: handle belongs to another table")
	ErrStaleHandle         = errors.New("table: handle generation is stale")
	ErrGenerationExhausted = errors.New("table: slot generation exhausted")
)

var nextTableID uint64

type SlotInfo struct {
	State      slot.State
	Generation uint64
}

type Snapshot struct {
	TableID                    uint64
	Slots                      []SlotInfo
	Free                       []int
	Live, FreeCount, Exhausted int
}

// Table is an in-memory, generation-checked handle table.
type Table[T any] struct {
	mu        sync.RWMutex
	id        uint64
	slots     []*slot.Slot[T]
	free      *freelist.List[T]
	live      int
	exhausted int
}

func New[T any](capacity int) (*Table[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	id := atomic.AddUint64(&nextTableID, 1)
	entries := make([]*slot.Slot[T], capacity)
	for i := range entries {
		entries[i] = slot.New[T]()
	}
	return &Table[T]{id: id, slots: entries, free: freelist.New(entries)}, nil
}

func (t *Table[T]) Cap() int { return len(t.slots) }

func (t *Table[T]) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.live
}

func (t *Table[T]) ID() uint64 { return t.id }

func (t *Table[T]) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	snap := Snapshot{
		TableID: t.id,
		Slots: make([]SlotInfo, len(t.slots)),
		Free: t.free.Indexes(),
		Live: t.live,
		FreeCount: t.free.Len(),
		Exhausted: t.exhausted,
	}
	for i, entry := range t.slots {
		snap.Slots[i] = SlotInfo{entry.State(), entry.Generation()}
	}
	return snap
}

func (t *Table[T]) Insert(value T) (handle.Handle, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	index, ok := t.free.Pop()
	if !ok {
		return handle.Handle{}, ErrTableFull
	}
	entry := t.slots[index]
	if !entry.Allocate(value) {
		return handle.Handle{}, ErrStaleHandle
	}
	t.live++
	return handle.Encode(t.id, uint64(index), entry.Generation()), nil
}

func (t *Table[T]) Get(h handle.Handle) (T, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	index, err := t.resolve(h)
	if err != nil {
		var zero T
		return zero, err
	}
	return t.slots[index].Value(), nil
}

func (t *Table[T]) Remove(h handle.Handle) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	index, err := t.resolve(h)
	if err != nil {
		return err
	}
	reusable := t.slots[index].Release(handle.MaxGeneration)
	t.live--
	if reusable {
		t.free.PushTail(index)
	} else {
		t.exhausted++
	}
	return nil
}

func (t *Table[T]) resolve(h handle.Handle) (int, error) {
	if h.IsZero() {
		return 0, ErrZeroHandle
	}
	tableID, index, generation := h.Decode()
	if tableID != t.id {
		return 0, ErrForeignTable
	}
	if index >= uint64(len(t.slots)) || !t.slots[index].Holds(generation) {
		return 0, ErrStaleHandle
	}
	return int(index), nil
}

func (t *Table[T]) retireSlot(index int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if index < 0 || index >= len(t.slots) || !t.slots[index].Retire(handle.MaxGeneration) {
		return ErrStaleHandle
	}
	t.live--
	t.exhausted++
	return nil
}

func (t *Table[T]) lastPopVisited() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return int(reflect.ValueOf(t.free).Elem().FieldByName("visited").Int())
}
