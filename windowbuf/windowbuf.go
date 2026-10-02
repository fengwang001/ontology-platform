// Package windowbuf implements a windowed result suppression buffer with a
// grace period and a capacity policy.
//
// Results are buffered per (key, window-start) and withheld until stream
// time passes the window end plus the grace period, at which point they are
// emitted as Final. When the buffer is full, entries are either emitted
// early (EmitEarly) or the offending update is rejected (Shutdown).
package windowbuf

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"
)

// Parameter bounds.
const (
	maxS     = int64(1_000_000_000)
	maxG     = int64(1_000_000_000)
	maxE     = int64(1_000_000)
	maxTs    = int64(1_000_000_000_000_000)
	maxValue = int64(1_000_000_000_000)
)

// Policy decides what happens when the buffer is full.
type Policy int

const (
	// EmitEarly evicts the (end, key)-smallest buffered entries as Early.
	EmitEarly Policy = iota
	// Shutdown rejects the update without changing any state.
	Shutdown
)

// Mark distinguishes Final from Early emissions.
type Mark int

const (
	// Early marks an entry evicted ahead of its close time.
	Early Mark = iota
	// Final marks an entry emitted at or after its close time.
	Final
)

func (m Mark) String() string {
	if m == Final {
		return "Final"
	}
	return "Early"
}

// Outcome is the result classification of an Update or Tick call.
type Outcome int

const (
	// OutcomeOK means the operation took effect.
	OutcomeOK Outcome = iota
	// OutcomeInvalid means a parameter was out of range.
	OutcomeInvalid
	// OutcomeLate means the update was dropped as late (and counted).
	OutcomeLate
	// OutcomeRejected means the operation was rejected without side effects.
	OutcomeRejected
)

func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "OK"
	case OutcomeInvalid:
		return "Invalid"
	case OutcomeLate:
		return "Late"
	case OutcomeRejected:
		return "Rejected"
	}
	return "Unknown"
}

func (p Policy) String() string {
	if p == Shutdown {
		return "SHUTDOWN"
	}
	return "EMIT_EARLY"
}

// Emitted is a single result emitted by Update or Tick.
type Emitted struct {
	Key    []byte
	Ws     int64
	Value  int64
	LastTs int64
	Mark   Mark
}

// Entry is a snapshot of one buffered result.
type Entry struct {
	Key    []byte
	Ws     int64
	Value  int64
	LastTs int64
}

// Buffer is a windowed result suppression buffer. All methods are safe for
// concurrent use and behave as if executed in some serial order.
type Buffer struct {
	mu      sync.Mutex
	s       int64
	g       int64
	e       int64
	policy  Policy
	st      int64
	entries map[bufKey]bufValue
	order   emitHeap
	late    int64
	peeks   int64
}

type bufKey struct {
	key string
	ws  int64
}

type bufValue struct {
	value  int64
	lastTs int64
}

// New constructs a Buffer. It returns an error if any parameter is out of
// range: S in [1, 1e9], G in [0, 1e9], E in [1, 1e6], policy valid.
func New(s, g, e int64, policy Policy) (*Buffer, error) {
	if s < 1 || s > maxS {
		return nil, fmt.Errorf("windowbuf: window size %d out of range [1, %d]", s, maxS)
	}
	if g < 0 || g > maxG {
		return nil, fmt.Errorf("windowbuf: grace period %d out of range [0, %d]", g, maxG)
	}
	if e < 1 || e > maxE {
		return nil, fmt.Errorf("windowbuf: capacity %d out of range [1, %d]", e, maxE)
	}
	if policy != EmitEarly && policy != Shutdown {
		return nil, fmt.Errorf("windowbuf: unknown policy %d", int(policy))
	}
	return &Buffer{
		s:       s,
		g:       g,
		e:       e,
		policy:  policy,
		st:      -1,
		entries: make(map[bufKey]bufValue),
	}, nil
}

// Update processes one result. See the package documentation for the exact
// order of checks: invalid, late, advance, capacity, apply.
func (b *Buffer) Update(key []byte, ts, value int64) (Outcome, []Emitted) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(key) == 0 || ts < 0 || ts > maxTs || value < -maxValue || value > maxValue {
		return OutcomeInvalid, nil
	}

	ws := ts / b.s * b.s
	end := ws + b.s
	if end+b.g <= b.st {
		b.late++
		return OutcomeLate, nil
	}

	st := b.st
	if ts > st {
		st = ts
	}

	closing := b.popClosing(st)

	k := bufKey{key: string(key), ws: ws}
	needNew := int64(1)
	if _, ok := b.entries[k]; ok {
		needNew = 0
	}

	var early []Emitted
	if rest := int64(len(b.entries)); rest+needNew > b.e {
		if b.policy == Shutdown {
			// rest+needNew > E implies the closing set was empty, so
			// nothing has been removed and no state has changed.
			return OutcomeRejected, nil
		}
		early = b.popEarly(rest + needNew - b.e)
	}

	b.entries[k] = bufValue{value: value, lastTs: ts}
	if needNew == 1 {
		heap.Push(&b.order, heapItem{end: end, key: k.key, ws: ws})
	}
	b.st = st

	return OutcomeOK, append(closing, early...)
}

// Tick advances stream time to t, emitting every entry whose close time is
// not greater than t.
func (b *Buffer) Tick(t int64) (Outcome, []Emitted) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if t < 0 || t > maxTs {
		return OutcomeInvalid, nil
	}
	if t < b.st {
		return OutcomeRejected, nil
	}
	if t == b.st {
		return OutcomeOK, nil
	}
	emitted := b.popClosing(t)
	b.st = t
	return OutcomeOK, emitted
}

// Buffered lists the buffered entries ordered by (end, key).
func (b *Buffer) Buffered() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]Entry, 0, len(b.entries))
	for _, item := range b.order {
		v := b.entries[bufKey{key: item.key, ws: item.ws}]
		out = append(out, Entry{
			Key:    []byte(item.key),
			Ws:     item.ws,
			Value:  v.value,
			LastTs: v.lastTs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		endI, endJ := out[i].Ws+b.s, out[j].Ws+b.s
		if endI != endJ {
			return endI < endJ
		}
		return string(out[i].Key) < string(out[j].Key)
	})
	return out
}

// StreamTime returns the current stream time.
func (b *Buffer) StreamTime() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st
}

// Late returns the number of dropped late updates.
func (b *Buffer) Late() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.late
}

// popClosing removes and returns every buffered entry whose close time
// (end+G) is not greater than st, in (end, key) order, marked Final.
func (b *Buffer) popClosing(st int64) []Emitted {
	var out []Emitted
	for len(b.order) > 0 {
		top := b.order[0]
		b.peeks++
		if top.end+b.g > st {
			break
		}
		heap.Pop(&b.order)
		out = append(out, b.remove(top, Final))
	}
	return out
}

// popEarly removes and returns the x (end, key)-smallest buffered entries,
// marked Early.
func (b *Buffer) popEarly(x int64) []Emitted {
	out := make([]Emitted, 0, x)
	for i := int64(0); i < x; i++ {
		b.peeks++
		top := heap.Pop(&b.order).(heapItem)
		out = append(out, b.remove(top, Early))
	}
	return out
}

func (b *Buffer) remove(item heapItem, mark Mark) Emitted {
	k := bufKey{key: item.key, ws: item.ws}
	v := b.entries[k]
	delete(b.entries, k)
	return Emitted{
		Key:    []byte(item.key),
		Ws:     item.ws,
		Value:  v.value,
		LastTs: v.lastTs,
		Mark:   mark,
	}
}
