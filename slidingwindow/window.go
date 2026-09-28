package slidingwindow

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
)

var (
	// ErrInvalidCapacity is returned when the window capacity is not positive.
	ErrInvalidCapacity = errors.New("slidingwindow: capacity must be a positive integer")
	// ErrEmptyWindow is returned when Evict or Max is called on an empty window.
	ErrEmptyWindow = errors.New("slidingwindow: window is empty")
	// ErrInvalidValue is returned when an appended value is NaN or Inf.
	ErrInvalidValue = errors.New("slidingwindow: value must be finite")
)

// entry is one slot of the circular ring buffer.
type entry struct {
	value float64
	// dqSlot is the ring-slot index of this element inside the monotonic
	// deque, or -1 when the element has already been displaced.
	dqSlot int
}

// dqNode is one node of the monotonic deque. Node indices are ring-slot
// indices, so each window element owns at most one node for its lifetime.
type dqNode struct {
	prev int
	next int
}

// Window is a fixed-capacity sliding window that reports the maximum
// value currently held. Every element is physically copied at most once
// out of the monotonic deque; eviction never rescans the window.
type Window struct {
	mu       sync.RWMutex
	capacity int

	ring []entry
	head int // index of the oldest element
	size int

	// Monotonic deque (values strictly decreasing front to back, ties kept).
	// head/tail hold ring-slot indices; -1 means empty.
	dq     []dqNode
	dqHead int
	dqTail int
	dqSize int
	moves  uint64 // elements displaced from the deque tail by newcomers
	logger *slog.Logger

	appends atomic.Uint64 // successful Appends, used by tests to bound runs
}

// New creates an empty window with the given capacity.
func New(capacity int, opts ...Option) (*Window, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidCapacity, capacity)
	}
	w := &Window{
		capacity: capacity,
		ring:     make([]entry, capacity),
		dq:       make([]dqNode, capacity),
		dqHead:   -1,
		dqTail:   -1,
		logger:   slog.Default(),
	}
	for i := range w.ring {
		w.ring[i].dqSlot = -1
	}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// Append adds one value, evicting the oldest value when full.
func (w *Window) Append(v float64) (evicted float64, evictedOK bool, err error) {
	if !isFinite(v) {
		return 0, false, fmt.Errorf("%w: got %v", ErrInvalidValue, v)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size == w.capacity {
		evicted = w.popFront()
		evictedOK = true
	}
	slot := (w.head + w.size) % w.capacity
	w.size++

	popped := 0
	for w.dqSize > 0 {
		back := w.ring[w.dqTail]
		if back.value < v {
			w.dqPopBack()
			w.moves++
			popped++
			continue
		}
		break
	}

	w.ring[slot] = entry{value: v, dqSlot: slot}
	w.dqPushBack(slot)
	w.appends.Add(1)

	w.logger.Info("append",
		slog.Float64("input", v),
		slog.Bool("evicted", evictedOK),
		slog.Float64("evicted_value", evicted),
		slog.Int("window_len", w.size),
		slog.Float64("max", w.ring[w.dqHead].value),
		slog.Int("tail_popped", popped),
		slog.String("basis", fmt.Sprintf("deque front after displacing %d smaller tail element(s)", popped)),
		slog.Any("deque_values", w.dequeValuesLocked()),
	)
	return evicted, evictedOK, nil
}

// Evict removes and returns the oldest value.
func (w *Window) Evict() (float64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size == 0 {
		return 0, fmt.Errorf("%w: cannot evict", ErrEmptyWindow)
	}
	v := w.popFront()

	args := []any{
		slog.String("op", "evict"),
		slog.Float64("evicted_value", v),
		slog.Int("window_len", w.size),
	}
	if w.size > 0 {
		args = append(args,
			slog.Float64("max", w.ring[w.dqHead].value),
			slog.String("basis", "deque front after dropping oldest element"),
			slog.Any("deque_values", w.dequeValuesLocked()),
		)
	} else {
		args = append(args, slog.String("basis", "window empty, no maximum"))
	}
	w.logger.Info("evict", args...)
	return v, nil
}

// Max returns the maximum value currently in the window.
func (w *Window) Max() (float64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.size == 0 {
		return 0, fmt.Errorf("%w: cannot read maximum", ErrEmptyWindow)
	}
	return w.ring[w.dqHead].value, nil
}

// BatchAppend appends every value as one atomic operation: if any value is
// invalid, the whole batch is rejected and no state changes.
func (w *Window) BatchAppend(values []float64) error {
	for i, v := range values {
		if !isFinite(v) {
			return fmt.Errorf("%w: at index %d, got %v", ErrInvalidValue, i, v)
		}
	}
	for _, v := range values {
		if _, _, err := w.Append(v); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot returns window contents from oldest to newest together with the
// maximum. Both are read under one lock, so the maximum always matches a
// scan of the returned slice.
func (w *Window) Snapshot() (values []float64, max float64, err error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.size == 0 {
		return nil, 0, fmt.Errorf("%w: cannot snapshot", ErrEmptyWindow)
	}
	values = make([]float64, w.size)
	for i := 0; i < w.size; i++ {
		values[i] = w.ring[(w.head+i)%w.capacity].value
	}
	return values, w.ring[w.dqHead].value, nil
}

// Len returns the number of elements currently held.
func (w *Window) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.size
}

// Cap returns the fixed window capacity.
func (w *Window) Cap() int {
	return w.capacity
}

// Moves returns how many elements have been displaced from the monotonic
// deque tail. Each element is displaced at most once; rejected operations
// never advance the counter.
func (w *Window) Moves() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.moves
}

// appendedCount reports how many successful Appends have occurred.
func (w *Window) appendedCount() uint64 {
	return w.appends.Load()
}

func (w *Window) popFront() float64 {
	slot := w.head
	v := w.ring[slot].value
	if w.ring[slot].dqSlot != -1 {
		w.dqRemove(slot)
	}
	w.ring[slot] = entry{dqSlot: -1}
	w.head = (w.head + 1) % w.capacity
	w.size--
	return v
}

func (w *Window) dqPushBack(slot int) {
	w.dq[slot] = dqNode{prev: w.dqTail, next: -1}
	if w.dqTail != -1 {
		w.dq[w.dqTail].next = slot
	} else {
		w.dqHead = slot
	}
	w.dqTail = slot
	w.dqSize++
}

func (w *Window) dqPopBack() {
	slot := w.dqTail
	prev := w.dq[slot].prev
	if prev != -1 {
		w.dq[prev].next = -1
	} else {
		w.dqHead = -1
	}
	w.dqTail = prev
	w.dqSize--
	w.ring[slot].dqSlot = -1
}

func (w *Window) dqRemove(slot int) {
	node := w.dq[slot]
	if node.prev != -1 {
		w.dq[node.prev].next = node.next
	} else {
		w.dqHead = node.next
	}
	if node.next != -1 {
		w.dq[node.next].prev = node.prev
	} else {
		w.dqTail = node.prev
	}
	w.dqSize--
	w.ring[slot].dqSlot = -1
}

func (w *Window) dequeValuesLocked() []float64 {
	out := make([]float64, 0, w.dqSize)
	for slot := w.dqHead; slot != -1; slot = w.dq[slot].next {
		out = append(out, w.ring[slot].value)
	}
	return out
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
