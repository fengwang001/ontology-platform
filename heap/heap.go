// Package heap implements a deterministic, concurrency-safe garbage
// collected heap model with soft, weak and phantom references plus
// finalization. Collection runs as one atomic six-phase step.
package heap

import (
	"errors"
	"math"
	"sort"
	"sync"
)

// Kind is the kind of a reference object.
type Kind int

const (
	Soft Kind = iota
	Weak
	Phantom
)

func (k Kind) valid() bool { return k == Soft || k == Weak || k == Phantom }

// Rejection reasons, distinguishable via errors.Is.
var (
	ErrInvalidParam    = errors.New("heap: invalid parameter")
	ErrNotFound        = errors.New("heap: object or queue not found")
	ErrKindMismatch    = errors.New("heap: kind mismatch")
	ErrClockRegression = errors.New("heap: clock regression")
	ErrHeapFull        = errors.New("heap: heap full")
)

const (
	maxCapacity  = int64(1_000_000_000_000)
	maxUnit      = int64(1_000_000_000)
	maxRetain    = int64(1_000_000_000)
	maxSize      = int64(1_000_000)
	maxFields    = 8
	maxNow       = int64(1_000_000_000_000_000)
	maxFinalizeN = 1_000_000
)

// CollectResult reports the outcome of one Collect.
type CollectResult struct {
	Collected        []int // ids of reclaimed objects, ascending
	SoftCleared      []int // refs cleared in phase 2, processing order
	WeakCleared      []int // refs cleared in phase 3, processing order
	FallbackCleared  []int // refs cleared in phase 5, processing order
	FinalizeSelected []int // objects newly selected in phase 4, ascending
	Used             int64 // heap usage after the sweep
}

type object struct {
	id          int
	size        int64
	ref         bool
	kind        Kind
	target      int
	queue       int
	timestamp   int64
	fields      []int
	finalizable bool
	inFinalQ    bool
	mark        int
}

type intQueue struct {
	items []int
	head  int
}

func (q *intQueue) push(id int) { q.items = append(q.items, id) }

func (q *intQueue) pop() (int, bool) {
	if q.head >= len(q.items) {
		return 0, false
	}
	id := q.items[q.head]
	q.head++
	return id, true
}

// Heap is a garbage-collected heap model. All methods are safe for
// concurrent use; every operation is atomic and the result is
// equivalent to some serial order.
type Heap struct {
	mu       sync.Mutex
	capacity int64
	unit     int64
	retain   int64
	used     int64
	nextID   int
	nextQ    int
	objects  map[int]*object
	roots    map[int]bool
	queues   map[int]*intQueue
	finalQ   []int
	maxNow   int64
	stamp    int
	visits   int
}

// New creates a heap with the given capacity C, soft-reference unit U
// and per-unit retain duration M.
func New(C, U, M int64) (*Heap, error) {
	if C < 1 || C > maxCapacity || U < 1 || U > maxUnit || M < 0 || M > maxRetain {
		return nil, ErrInvalidParam
	}
	return &Heap{
		capacity: C,
		unit:     U,
		retain:   M,
		nextID:   1,
		nextQ:    1,
		objects:  make(map[int]*object),
		roots:    make(map[int]bool),
		queues:   make(map[int]*intQueue),
	}, nil
}

// Used returns the sum of sizes of all live objects.
func (h *Heap) Used() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.used
}

// Alloc allocates an ordinary object.
func (h *Heap) Alloc(s int64, f int, finalizable bool) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s < 1 || s > maxSize || f < 0 || f > maxFields {
		return 0, ErrInvalidParam
	}
	if h.used+s > h.capacity {
		return 0, ErrHeapFull
	}
	id := h.nextID
	h.nextID++
	h.objects[id] = &object{
		id:          id,
		size:        s,
		fields:      make([]int, f),
		finalizable: finalizable,
	}
	h.used += s
	return id, nil
}

// NewRef allocates a reference object.
func (h *Heap) NewRef(kind Kind, target, queue int, s int64, now int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !kind.valid() || s < 1 || s > maxSize || now < 0 || now > maxNow {
		return 0, ErrInvalidParam
	}
	if target != 0 {
		if _, ok := h.objects[target]; !ok {
			return 0, ErrNotFound
		}
	}
	if queue != 0 {
		if _, ok := h.queues[queue]; !ok {
			return 0, ErrNotFound
		}
	}
	if now < h.maxNow {
		return 0, ErrClockRegression
	}
	if h.used+s > h.capacity {
		return 0, ErrHeapFull
	}
	id := h.nextID
	h.nextID++
	h.objects[id] = &object{
		id:        id,
		size:      s,
		ref:       true,
		kind:      kind,
		target:    target,
		queue:     queue,
		timestamp: now,
	}
	h.used += s
	h.maxNow = now
	return id, nil
}

// CreateQueue creates a reference queue and returns its number.
func (h *Heap) CreateQueue() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	q := h.nextQ
	h.nextQ++
	h.queues[q] = &intQueue{}
	return q
}

// SetField sets field i of ordinary object o to t (0 clears it).
func (h *Heap) SetField(o, i, t int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i < 0 || i >= maxFields {
		return ErrInvalidParam
	}
	obj, ok := h.objects[o]
	if !ok {
		return ErrNotFound
	}
	if t != 0 {
		if _, ok := h.objects[t]; !ok {
			return ErrNotFound
		}
	}
	if obj.ref {
		return ErrKindMismatch
	}
	if i >= len(obj.fields) {
		return ErrInvalidParam
	}
	obj.fields[i] = t
	return nil
}

// SetRoot adds o to the root set.
func (h *Heap) SetRoot(o int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.objects[o]; !ok {
		return ErrNotFound
	}
	h.roots[o] = true
	return nil
}

// ClearRoot removes o from the root set.
func (h *Heap) ClearRoot(o int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.objects[o]; !ok {
		return ErrNotFound
	}
	delete(h.roots, o)
	return nil
}

// Get returns the target of reference r (0 if cleared or nil).
func (h *Heap) Get(r int, now int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if now < 0 || now > maxNow {
		return 0, ErrInvalidParam
	}
	obj, ok := h.objects[r]
	if !ok {
		return 0, ErrNotFound
	}
	if !obj.ref {
		return 0, ErrKindMismatch
	}
	if now < h.maxNow {
		return 0, ErrClockRegression
	}
	h.maxNow = now
	if obj.kind == Soft {
		obj.timestamp = now
	}
	return obj.target, nil
}

// Poll removes and returns the oldest enqueued reference of queue q,
// or 0 when the queue is empty.
func (h *Heap) Poll(q int) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue, ok := h.queues[q]
	if !ok {
		return 0, ErrNotFound
	}
	id, _ := queue.pop()
	return id, nil
}

// Finalize takes up to n objects from the finalization queue (FIFO),
// clears their finalizable flag and returns their ids.
func (h *Heap) Finalize(n int) ([]int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n < 0 || n > maxFinalizeN {
		return nil, ErrInvalidParam
	}
	k := n
	if k > len(h.finalQ) {
		k = len(h.finalQ)
	}
	out := make([]int, 0, k)
	for _, id := range h.finalQ[:k] {
		obj := h.objects[id]
		obj.finalizable = false
		obj.inFinalQ = false
		out = append(out, id)
	}
	h.finalQ = h.finalQ[k:]
	return out, nil
}

// Collect runs one atomic six-phase collection.
func (h *Heap) Collect(now int64) (CollectResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if now < 0 || now > maxNow {
		return CollectResult{}, ErrInvalidParam
	}
	if now < h.maxNow {
		return CollectResult{}, ErrClockRegression
	}
	h.maxNow = now

	res := CollectResult{
		Collected:        []int{},
		SoftCleared:      []int{},
		WeakCleared:      []int{},
		FallbackCleared:  []int{},
		FinalizeSelected: []int{},
	}

	h.stamp++
	h.visits = 0

	// Phase 1: strong marking from roots and the finalization queue.
	for id := range h.roots {
		h.markFrom(id)
	}
	for _, id := range h.finalQ {
		h.markFrom(id)
	}
	var used0 int64
	for _, obj := range h.objects {
		if obj.mark == h.stamp {
			used0 += obj.size
		}
	}

	// Phase 2: soft references. free is fixed at the start of the phase.
	free := h.capacity - used0
	for _, obj := range h.markedRefs(Soft) {
		t := obj.target
		if t == 0 || h.marked(t) {
			continue
		}
		if softRetain(free, h.unit, h.retain, now-obj.timestamp) {
			h.markFrom(t)
		} else {
			h.clearAndEnqueue(obj, &res.SoftCleared)
		}
	}

	// Phase 3: weak references.
	for _, obj := range h.markedRefs(Weak) {
		t := obj.target
		if t == 0 || h.marked(t) {
			continue
		}
		h.clearAndEnqueue(obj, &res.WeakCleared)
	}

	// Phase 4: finalization. The selection set is fixed before any
	// resurrection marking happens.
	var selected []int
	for id, obj := range h.objects {
		if !obj.ref && obj.mark != h.stamp && obj.finalizable && !obj.inFinalQ {
			selected = append(selected, id)
		}
	}
	sort.Ints(selected)
	for _, id := range selected {
		h.objects[id].inFinalQ = true
		h.finalQ = append(h.finalQ, id)
		res.FinalizeSelected = append(res.FinalizeSelected, id)
	}
	for _, id := range selected {
		h.markFrom(id)
	}

	// Phase 5: fallback for any marked reference whose target is still
	// unmarked.
	for _, obj := range h.markedRefs(-1) {
		t := obj.target
		if t == 0 || h.marked(t) {
			continue
		}
		h.clearAndEnqueue(obj, &res.FallbackCleared)
	}

	// Phase 6: sweep every unmarked object.
	for id, obj := range h.objects {
		if obj.mark != h.stamp {
			res.Collected = append(res.Collected, id)
			h.used -= obj.size
			delete(h.objects, id)
			delete(h.roots, id)
		}
	}
	sort.Ints(res.Collected)
	res.Used = h.used
	return res, nil
}

// markFrom marks id and, for ordinary objects, everything reachable
// through fields. Reference objects are marked but their targets are
// not traversed. Each object is visited at most once per Collect.
func (h *Heap) markFrom(id int) {
	if id == 0 {
		return
	}
	stack := []int{id}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		obj := h.objects[x]
		if obj == nil || obj.mark == h.stamp {
			continue
		}
		obj.mark = h.stamp
		h.visits++
		if !obj.ref {
			stack = append(stack, obj.fields...)
		}
	}
}

func (h *Heap) marked(id int) bool {
	obj := h.objects[id]
	return obj != nil && obj.mark == h.stamp
}

// markedRefs returns the marked reference objects of the given kind
// (any kind when kind is negative), sorted by ascending id.
func (h *Heap) markedRefs(kind Kind) []*object {
	var out []*object
	for _, obj := range h.objects {
		if !obj.ref || obj.mark != h.stamp {
			continue
		}
		if kind >= 0 && obj.kind != kind {
			continue
		}
		out = append(out, obj)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (h *Heap) clearAndEnqueue(obj *object, out *[]int) {
	obj.target = 0
	if obj.queue != 0 {
		h.queues[obj.queue].push(obj.id)
	}
	*out = append(*out, obj.id)
}

// softRetain reports whether a soft reference is retained for the
// given elapsed time: elapsed <= floor(free/U) * M.
func softRetain(free, unit, retain, elapsed int64) bool {
	q := free / unit
	if retain != 0 && q > math.MaxInt64/retain {
		return true // threshold exceeds any representable elapsed time
	}
	return elapsed <= q*retain
}
