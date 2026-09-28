package asyncbuf

import (
	"sync"
)

// Mode selects the output ordering policy.
type Mode int

const (
	Ordered   Mode = iota // strict input order
	Unordered             // completion order within watermark-delimited segments
)

// EventKind distinguishes elements from watermarks in the output stream.
type EventKind int

const (
	ElementEvent EventKind = iota
	WatermarkEvent
)

// Event is one output item: either an element payload or a watermark.
type Event struct {
	Kind      EventKind
	ID        string
	Value     any
	Watermark int64
}

// Logger receives step-by-step decisions. All methods must be safe for
// concurrent use; the standard log.Logger satisfies this contract.
type Logger interface {
	Printf(format string, args ...any)
}

// Option configures a Buffer at construction.
type Option func(*Buffer)

// WithLogger attaches a decision logger.
func WithLogger(l Logger) Option {
	return func(b *Buffer) { b.log = l }
}

type nodeKind int

const (
	nodeElement nodeKind = iota
	nodeWatermark
)

// node is one position in the input FIFO: an element or a watermark.
type node struct {
	kind      nodeKind
	id        string
	value     any
	watermark int64
	done      bool
	open      bool // unordered: segment currently releasing
	next      *node
	doneNext  *node // unordered: completion-order queue link
}

// Buffer is a concurrent async I/O output buffer.
//
// Elements occupy one unit of capacity until they are emitted. Completion is
// declared asynchronously and may arrive out of order. Watermarks never
// occupy capacity and must be strictly increasing.
type Buffer struct {
	mode     Mode
	capacity int

	mu        sync.Mutex
	head      *node
	tail      *node
	known     map[string]*node
	occupied  int
	seq       uint64
	lastWm    int64
	wmSeen    bool
	closed    bool
	outSeq    uint64
	pendingWm int
	doneHead  *node // unordered: completed elements in completion order
	doneTail  *node

	cond *sync.Cond
	log  Logger
}

// NewBuffer creates a buffer. capacity must be positive and counts buffered,
// not-yet-emitted elements.
func NewBuffer(mode Mode, capacity int, opts ...Option) (*Buffer, error) {
	if mode != Ordered && mode != Unordered {
		return nil, &RejectError{Cause: CauseInvalidArgument, Message: "unknown mode"}
	}
	if capacity <= 0 {
		return nil, &RejectError{Cause: CauseInvalidArgument, Message: "capacity must be positive"}
	}
	b := &Buffer{mode: mode, capacity: capacity, known: make(map[string]*node)}
	b.cond = sync.NewCond(&b.mu)
	for _, opt := range opts {
		opt(b)
	}
	b.debugf("init mode=%s capacity=%d", modeName(mode), capacity)
	return b, nil
}

// Push admits an element. It is rejected atomically (no state change) when
// capacity is full, the id is empty/duplicate, or the buffer is closed.
func (b *Buffer) Push(id string, value any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return b.rejectLocked(CauseClosed, "push after close: id=%q", id)
	}
	if id == "" {
		return b.rejectLocked(CauseEmptyID, "push with empty id")
	}
	if _, ok := b.known[id]; ok {
		return b.rejectLocked(CauseDuplicateID, "duplicate id %q", id)
	}
	if b.occupied >= b.capacity {
		return b.rejectLocked(CauseCapacityFull, "capacity full: occupied=%d capacity=%d", b.occupied, b.capacity)
	}
	b.seq++
	n := &node{kind: nodeElement, id: id, value: value, open: b.mode == Unordered && b.pendingWm == 0}
	b.appendLocked(n)
	b.known[id] = n
	b.occupied++
	b.debugf("push id=%q seq=%d occupied=%d/%d open=%v", id, b.seq, b.occupied, b.capacity, n.open)
	b.emitReadyLocked("push:" + id)
	return nil
}

// Watermark injects a barrier. It must be strictly greater than the previous
// watermark.
func (b *Buffer) Watermark(wm int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return b.rejectLocked(CauseClosed, "watermark after close: %d", wm)
	}
	if b.wmSeen && wm <= b.lastWm {
		return b.rejectLocked(CauseWatermarkNotInc, "watermark %d not greater than %d", wm, b.lastWm)
	}
	b.seq++
	n := &node{kind: nodeWatermark, watermark: wm}
	b.appendLocked(n)
	b.pendingWm++
	b.lastWm = wm
	b.wmSeen = true
	b.debugf("watermark wm=%d seq=%d", wm, b.seq)
	b.emitReadyLocked("watermark")
	return nil
}

// Complete declares that an admitted element has finished. It may be called
// once per element; a second call reports already_completed, and an id never
// admitted (or already emitted) reports unknown_id.
func (b *Buffer) Complete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return b.rejectLocked(CauseClosed, "complete after close: id=%q", id)
	}
	if id == "" {
		return b.rejectLocked(CauseEmptyID, "complete with empty id")
	}
	n, ok := b.known[id]
	if !ok {
		return b.rejectLocked(CauseUnknownID, "unknown id %q", id)
	}
	if n.done {
		return b.rejectLocked(CauseAlreadyCompleted, "id %q already completed", id)
	}
	n.done = true
	if b.mode == Unordered {
		n.doneNext = nil
		if b.doneTail == nil {
			b.doneHead, b.doneTail = n, n
		} else {
			b.doneTail.doneNext = n
			b.doneTail = n
		}
	}
	b.debugf("complete id=%q open=%v", id, n.open)
	b.emitReadyLocked("complete:" + id)
	return nil
}

// Poll returns the next ready event without blocking. ok is false when nothing
// can currently be emitted (callers must distinguish a closed buffer via
// Drain/Recv or Closed).
func (b *Buffer) Poll() (ev Event, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ev, ok := b.nextEventLocked(); ok {
		b.popLocked()
		return ev, true
	}
	return Event{}, false
}

// Drain returns and removes all currently ready events (possibly empty).
func (b *Buffer) Drain() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Event
	for {
		ev, ok := b.nextEventLocked()
		if !ok {
			return out
		}
		out = append(out, ev)
		b.popLocked()
	}
}

// Recv blocks until an event is ready. After Close it drains remaining events
// first; once empty it returns ok=false.
func (b *Buffer) Recv() (ev Event, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if ev, ok := b.nextEventLocked(); ok {
			b.popLocked()
			return ev, true
		}
		if b.closed {
			return Event{}, false
		}
		b.cond.Wait()
	}
}

// Close forbids further input. Buffered, ready events remain retrievable.
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.debugf("close occupied=%d buffered=%d", b.occupied, b.lenLocked())
	b.cond.Broadcast()
}

// Occupied returns the number of admitted but not yet emitted elements.
func (b *Buffer) Occupied() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.occupied
}

// Capacity returns the configured capacity.
func (b *Buffer) Capacity() int { return b.capacity }

// Closed reports whether Close was called.
func (b *Buffer) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func (b *Buffer) appendLocked(n *node) {
	if b.tail == nil {
		b.head, b.tail = n, n
		return
	}
	b.tail.next = n
	b.tail = n
}

func (b *Buffer) lenLocked() int {
	count := 0
	for n := b.head; n != nil; n = n.next {
		count++
	}
	return count
}

// nextEventLocked decides the next output without mutating state.
func (b *Buffer) nextEventLocked() (Event, bool) {
	switch b.mode {
	case Ordered:
		n := b.head
		if n == nil {
			return Event{}, false
		}
		if n.kind == nodeWatermark {
			return Event{Kind: WatermarkEvent, Watermark: n.watermark}, true
		}
		if n.done {
			return Event{Kind: ElementEvent, ID: n.id, Value: n.value}, true
		}
		return Event{}, false
	case Unordered:
		// A ready head watermark means every preceding element was emitted:
		// emit it immediately and cascade.
		if b.head != nil && b.head.kind == nodeWatermark {
			return Event{Kind: WatermarkEvent, Watermark: b.head.watermark}, true
		}
		// Otherwise the oldest completion that is still buffered and lies in
		// the open segment is released (completion order, not input order).
		for n := b.doneHead; n != nil; n = n.doneNext {
			if n.kind == nodeElement && b.known[n.id] == n && n.open {
				return Event{Kind: ElementEvent, ID: n.id, Value: n.value}, true
			}
		}
		return Event{}, false
	}
	return Event{}, false
}

// popLocked removes the node corresponding to the event returned by
// nextEventLocked and performs unordered cascading.
func (b *Buffer) popLocked() {
	ev, ok := b.nextEventLocked()
	if !ok {
		return
	}
	if ev.Kind == ElementEvent {
		b.removeElementLocked(ev.ID)
		return
	}
	// Watermark at head: unlink it, then the segment after it becomes open.
	wm := b.head.watermark
	b.head = b.head.next
	if b.head == nil {
		b.tail = nil
	}
	b.pendingWm--
	if b.mode == Unordered {
		for n := b.head; n != nil; n = n.next {
			if n.kind == nodeWatermark {
				break
			}
			n.open = true
		}
	}
	b.outSeq++
	b.debugf("emit #%d watermark=%d reason=all-prior-elements-done", b.outSeq, wm)
	b.cond.Broadcast()
}

func (b *Buffer) removeElementLocked(id string) {
	var prev *node
	for n := b.head; n != nil; prev, n = n, n.next {
		if n.kind != nodeElement || n.id != id {
			continue
		}
		if prev == nil {
			b.head = n.next
		} else {
			prev.next = n.next
		}
		if n == b.tail {
			b.tail = prev
		}
		delete(b.known, id)
		b.compactDoneQLocked()
		b.occupied--
		b.outSeq++
		b.debugf("emit #%d element=%q reason=%s occupied=%d/%d",
			b.outSeq, id, b.emitReasonLocked(), b.occupied, b.capacity)
		b.cond.Broadcast()
		return
	}
}

func (b *Buffer) emitReasonLocked() string {
	if b.mode == Ordered {
		return "head-of-line-done"
	}
	if b.doneHead != nil && b.doneHead.open && b.known[b.doneHead.id] == b.doneHead {
		return "open-segment-completed"
	}
	return "cascaded-release"
}

// compactDoneQLocked lazily drops emitted nodes from the completion queue.
func (b *Buffer) compactDoneQLocked() {
	for b.doneHead != nil {
		if n := b.doneHead; n.kind == nodeElement && b.known[n.id] == n {
			return
		}
		b.doneHead = b.doneHead.doneNext
	}
	b.doneTail = nil
}

// emitReadyLocked recomputes readiness after a state change and logs the
// decision; actual emission happens on Poll/Drain/Recv. It wakes waiters
// whenever new events may be ready or the buffer was closed.
func (b *Buffer) emitReadyLocked(trigger string) {
	if _, ok := b.nextEventLocked(); ok {
		b.debugf("ready trigger=%s", trigger)
		b.cond.Broadcast()
	}
}

func (b *Buffer) rejectLocked(cause RejectCause, format string, args ...any) error {
	e := &RejectError{Cause: cause, Message: sprintf(format, args...)}
	b.debugf("reject cause=%s detail=%q (state unchanged)", cause, e.Message)
	return e
}

func (b *Buffer) debugf(format string, args ...any) {
	if b.log != nil {
		b.log.Printf("[asyncbuf] "+format, args...)
	}
}

func modeName(m Mode) string {
	if m == Ordered {
		return "ordered"
	}
	return "unordered"
}
