package refheap

import "sync"

// RefKind identifies a reference-object kind.
type RefKind int

const (
	Soft RefKind = iota + 1
	Weak
	Phantom
)

// ErrorCode enumerates the rejection reasons in the order in which a single
// operation must report the first matching failure.
type ErrorCode int

const (
	ErrInvalidArg ErrorCode = iota + 1
	ErrNotFound
	ErrWrongKind
	ErrClockBackward
	ErrHeapFull
)

// HeapError is returned by every rejected operation.
type HeapError struct{ Code ErrorCode }

func (e *HeapError) Error() string {
	switch e.Code {
	case ErrInvalidArg:
		return "refheap: invalid argument"
	case ErrNotFound:
		return "refheap: object or queue not found"
	case ErrWrongKind:
		return "refheap: wrong object kind"
	case ErrClockBackward:
		return "refheap: clock moved backwards"
	case ErrHeapFull:
		return "refheap: heap full"
	}
	return "refheap: unknown error"
}

// CollectResult is the atomic outcome of one Collect call.
type CollectResult struct {
	Reclaimed   []int64 // objects reclaimed in sweep, ascending ids
	SoftCleared []int64 // soft-reference ids cleared/enqueued in phase 2
	WeakCleared []int64 // weak-reference ids cleared/enqueued in phase 3
	CatchAll    []int64 // reference ids cleared/enqueued in phase 5 (any kind)
	Finalized   []int64 // ordinary objects newly selected in phase 4, ascending
	Used        int64   // used bytes after reclamation
}

const (
	maxSize   = int64(1_000_000)
	maxFields = 8
	maxNow    = int64(1_000_000_000_000_000)
	maxFinalN = 1_000_000
)

type object struct {
	id   int64
	size int64

	isRef       bool
	kind        RefKind
	target      int64 // referent; 0 after the reference is cleared
	queue       int64 // 0 means the reference has no queue
	timestamp   int64 // last access time for soft references
	fields      []int64
	finalizable bool
}

// Heap is the concurrent heap+collector model. A single mutex makes every
// operation linearizable; in particular each Collect is one atomic step.
type Heap struct {
	mu sync.Mutex

	capacity int64
	softUnit int64
	softKeep int64

	nextID int64 // shared sequence for ordinary and reference objects
	nextQ  int64
	maxNow int64
	used   int64

	objs        map[int64]*object
	roots       map[int64]struct{}
	queues      map[int64][]int64 // queue id -> FIFO of reference object ids
	finalQueue  []int64
	finalQueued map[int64]struct{}

	lastHits int // objects marked during the most recent Collect
}

// NewHeap validates C (1..1e12), U (1..1e9) and M (0..1e9).
func NewHeap(c, u, m int64) (*Heap, error) {
	if c < 1 || c > 1_000_000_000_000 ||
		u < 1 || u > 1_000_000_000 ||
		m < 0 || m > 1_000_000_000 {
		return nil, &HeapError{Code: ErrInvalidArg}
	}
	return &Heap{
		capacity:    c,
		softUnit:    u,
		softKeep:    m,
		objs:        map[int64]*object{},
		roots:       map[int64]struct{}{},
		queues:      map[int64][]int64{},
		finalQueued: map[int64]struct{}{},
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// Alloc creates an ordinary object and rejects if used+s would exceed C.
func (h *Heap) Alloc(size int64, fields int, finalizable bool) (int64, error) {
	if size < 1 || size > maxSize || fields < 0 || fields > maxFields {
		return 0, &HeapError{Code: ErrInvalidArg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.used+size > h.capacity {
		return 0, &HeapError{Code: ErrHeapFull}
	}
	h.nextID++
	h.objs[h.nextID] = &object{
		id:          h.nextID,
		size:        size,
		fields:      make([]int64, fields),
		finalizable: finalizable,
	}
	h.used += size
	return h.nextID, nil
}

// CreateQueue returns queue ids starting from 1.
func (h *Heap) CreateQueue() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextQ++
	h.queues[h.nextQ] = nil
	return h.nextQ
}

// NewRef allocates a reference object. target/queue of 0 mean null / no queue.
func (h *Heap) NewRef(kind RefKind, target, queue, size, now int64) (int64, error) {
	if kind < Soft || kind > Phantom || size < 1 || size > maxSize || !validNow(now) {
		return 0, &HeapError{Code: ErrInvalidArg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if target != 0 {
		if _, ok := h.objs[target]; !ok {
			return 0, &HeapError{Code: ErrNotFound}
		}
	}
	if queue != 0 {
		if _, ok := h.queues[queue]; !ok {
			return 0, &HeapError{Code: ErrNotFound}
		}
	}
	if now < h.maxNow {
		return 0, &HeapError{Code: ErrClockBackward}
	}
	if h.used+size > h.capacity {
		return 0, &HeapError{Code: ErrHeapFull}
	}
	h.maxNow = now
	h.nextID++
	h.objs[h.nextID] = &object{
		id:        h.nextID,
		size:      size,
		isRef:     true,
		kind:      kind,
		target:    target,
		queue:     queue,
		timestamp: now,
	}
	h.used += size
	return h.nextID, nil
}

// SetField points ordinary object o's field i at t (0 clears the field).
func (h *Heap) SetField(o int64, i int, t int64) error {
	if i < 0 || i >= maxFields {
		return &HeapError{Code: ErrInvalidArg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	obj, ok := h.objs[o]
	if !ok {
		return &HeapError{Code: ErrNotFound}
	}
	if obj.isRef {
		return &HeapError{Code: ErrWrongKind}
	}
	if i >= len(obj.fields) {
		return &HeapError{Code: ErrInvalidArg}
	}
	if t != 0 {
		if _, ok := h.objs[t]; !ok {
			return &HeapError{Code: ErrNotFound}
		}
	}
	obj.fields[i] = t
	return nil
}

// SetRoot adds o to the root set (idempotent).
func (h *Heap) SetRoot(o int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.objs[o]; !ok {
		return &HeapError{Code: ErrNotFound}
	}
	h.roots[o] = struct{}{}
	return nil
}

// ClearRoot removes o from the root set (idempotent).
func (h *Heap) ClearRoot(o int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.objs[o]; !ok {
		return &HeapError{Code: ErrNotFound}
	}
	delete(h.roots, o)
	return nil
}

// Get returns a reference's target; soft references refresh their timestamp.
func (h *Heap) Get(r, now int64) (int64, error) {
	if !validNow(now) {
		return 0, &HeapError{Code: ErrInvalidArg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	obj, ok := h.objs[r]
	if !ok {
		return 0, &HeapError{Code: ErrNotFound}
	}
	if !obj.isRef {
		return 0, &HeapError{Code: ErrWrongKind}
	}
	if now < h.maxNow {
		return 0, &HeapError{Code: ErrClockBackward}
	}
	h.maxNow = now
	if obj.kind == Soft {
		obj.timestamp = now
	}
	return obj.target, nil
}

// Poll removes the oldest reference id from queue q; 0 means the queue is empty.
func (h *Heap) Poll(q int64) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	qq, ok := h.queues[q]
	if !ok {
		return 0, &HeapError{Code: ErrNotFound}
	}
	if len(qq) == 0 {
		return 0, nil
	}
	id := qq[0]
	h.queues[q] = qq[1:]
	return id, nil
}

// Finalize pops at most n entries from the FIFO finalization queue and clears
// their finalizable flags.
func (h *Heap) Finalize(n int) ([]int64, error) {
	if n < 0 || n > maxFinalN {
		return nil, &HeapError{Code: ErrInvalidArg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []int64{}
	if n == 0 || len(h.finalQueue) == 0 {
		return out, nil
	}
	if n > len(h.finalQueue) {
		n = len(h.finalQueue)
	}
	out = append(out, h.finalQueue[:n]...)
	h.finalQueue = h.finalQueue[n:]
	for _, id := range out {
		if obj, ok := h.objs[id]; ok {
			obj.finalizable = false
		}
		delete(h.finalQueued, id)
	}
	return out, nil
}

// Used reports the total size of every object not yet reclaimed.
func (h *Heap) Used() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.used
}

// LastMarkVisitCount reports how many objects were first-marked during the
// last Collect; because every object is marked at most once it equals the
// number of survivors.
func (h *Heap) LastMarkVisitCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastHits
}
