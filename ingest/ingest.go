// Package ingest owns the per-device telemetry state: the contiguous
// watermark, the device buffer bounds, and the final disposition (Received,
// Lost or still Missing) of every sequence number up to hi.
//
// Missing sequence numbers are kept in two seqset.Set structures: req
// (requestable, not in flight) and inl (in flight inside some request);
// lost sequence numbers are kept in a third set so that late arrivals can
// be told apart from duplicates. The planning policy itself lives in the
// plan package, which drives the atomic primitives exported here.
package ingest

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/seqset"
)

// Bounds of the domain.
const (
	MaxSeq = int64(1_000_000_000_000) // sequence numbers are 1..MaxSeq
	MaxNow = int64(1_000_000_000_000) // timestamps are 0..MaxNow ms
)

// Errors reported by the engine, in the precedence order they are checked:
// ErrInvalid > ErrClockBack > ErrNoDevice > ErrRegress.
var (
	ErrInvalid   = errors.New("ingest: invalid argument")
	ErrClockBack = errors.New("ingest: clock moved backwards")
	ErrNoDevice  = errors.New("ingest: device not registered")
	ErrRegress   = errors.New("ingest: buffer range regresses")
	ErrExists    = errors.New("ingest: device already registered")
)

// Params holds the immutable planner configuration.
type Params struct {
	Lm int   // max length of a single request, 1..1e4
	K  int   // max requests per Plan call, 1..100
	Tq int64 // request timeout in ms, 1..1e9
	R  int   // max times a sequence number may be requested, 1..10
}

func (p Params) valid() bool {
	return p.Lm >= 1 && p.Lm <= 10_000 &&
		p.K >= 1 && p.K <= 100 &&
		p.Tq >= 1 && p.Tq <= 1_000_000_000 &&
		p.R >= 1 && p.R <= 10
}

// Request is one backfill request for the inclusive range [Lo, Hi].
type Request struct {
	Lo, Hi int64
}

// Stats is a consistent snapshot of a device's counters.
type Stats struct {
	Hi, Lo, F                          int64
	Received, Lost, Missing, Dup, Late int64
}

// Gap describes the front requestable segment.
type Gap struct {
	Lo, Hi int64
	C      int // times every sequence number in the gap has been requested
}

// heapEntry references an in-flight segment by (start, generation).
type heapEntry struct {
	q, start, gen int64
}

type inflightHeap []heapEntry

func (h inflightHeap) Len() int { return len(h) }
func (h inflightHeap) Less(i, j int) bool {
	if h[i].q != h[j].q {
		return h[i].q < h[j].q
	}
	return h[i].start < h[j].start
}
func (h inflightHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *inflightHeap) Push(x any)   { *h = append(*h, x.(heapEntry)) }
func (h *inflightHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// Device holds the full state of one registered device.
type Device struct {
	hi, lo   int64
	req      seqset.Set // missing, requestable; adjacent segments differ in C
	inl      seqset.Set // missing, in flight
	lost     seqset.Set // lost (terminal), attributes unused
	received int64
	lostN    int64
	dup      int64
	late     int64
	inflight inflightHeap

	planExamined int // segments examined by the last Plan
	planTimeouts int // segments settled by the last Plan
}

// Engine is the concurrency-safe container of all devices. All operations
// take the engine lock, so concurrent calls are linearizable.
type Engine struct {
	mu     sync.Mutex
	p      Params
	maxNow int64 // largest accepted now; -1 means none
	gen    int64
	devs   map[string]*Device
}

// New validates the parameters and returns an empty engine.
func New(p Params) (*Engine, error) {
	if !p.valid() {
		return nil, ErrInvalid
	}
	return &Engine{p: p, maxNow: -1, devs: make(map[string]*Device)}, nil
}

// Params returns the engine configuration.
func (e *Engine) Params() Params { return e.p }

// Register creates a device. A duplicate name reports ErrExists.
func (e *Engine) Register(dev string) error {
	if dev == "" {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.devs[dev]; ok {
		return ErrExists
	}
	e.devs[dev] = &Device{lo: 1}
	return nil
}

// Lock and Unlock serialize operations; they exist so that package plan can
// compose the exported device primitives into one atomic operation.
func (e *Engine) Lock()   { e.mu.Lock() }
func (e *Engine) Unlock() { e.mu.Unlock() }

// CheckClock reports ErrClockBack if now is below the largest accepted now.
// The caller must hold the lock.
func (e *Engine) CheckClock(now int64) error {
	if now < e.maxNow {
		return ErrClockBack
	}
	return nil
}

// AcceptNow records now as the largest accepted timestamp. The caller must
// hold the lock and only call this for operations that were not rejected.
func (e *Engine) AcceptNow(now int64) { e.maxNow = now }

// Device returns the device or false. The caller must hold the lock.
func (e *Engine) Device(dev string) (*Device, bool) {
	d, ok := e.devs[dev]
	return d, ok
}

// NextGen returns a fresh generation counter. The caller must hold the lock.
func (e *Engine) NextGen() int64 {
	e.gen++
	return e.gen
}

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// Ingest records the arrival of seq from dev at time now.
func (e *Engine) Ingest(dev string, seq, now int64) error {
	if seq < 1 || seq > MaxSeq || !validNow(now) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.CheckClock(now); err != nil {
		return err
	}
	d, ok := e.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	d.ingest(seq, e)
	e.maxNow = now
	return nil
}

func (d *Device) ingest(seq int64, e *Engine) {
	if seq > d.hi {
		if seq > d.hi+1 {
			d.req.AddMerged(seqset.Interval{Start: d.hi + 1, End: seq - 1})
		}
		d.hi = seq
		d.received++
		return
	}
	if idx, ok := d.req.Find(seq); ok {
		d.req.RemovePointAt(idx, seq)
		d.received++
		return
	}
	if idx, ok := d.inl.Find(seq); ok {
		_, lo, hi := d.inl.RemovePointAt(idx, seq)
		// The surviving pieces stay in flight with the same timestamp but
		// need fresh generations so stale heap entries cannot match them.
		for i := lo; i < hi; i++ {
			iv := d.inl.At(i)
			iv.Gen = e.NextGen()
			d.inl.SetAttr(i, iv.Attr)
			heap.Push(&d.inflight, heapEntry{q: iv.Q, start: iv.Start, gen: iv.Gen})
		}
		d.received++
		return
	}
	if _, ok := d.lost.Find(seq); ok {
		d.late++
		return
	}
	d.dup++
}

// Hello accepts the device's declaration that its local buffer currently
// holds [lo2, hi2] (lo2 == hi2+1 means empty).
func (e *Engine) Hello(dev string, lo2, hi2, now int64) error {
	if lo2 < 1 || lo2 > hi2+1 || hi2 < 0 || hi2 > MaxSeq || !validNow(now) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.CheckClock(now); err != nil {
		return err
	}
	d, ok := e.devs[dev]
	if !ok {
		return ErrNoDevice
	}
	if lo2 < d.lo || hi2 < d.hi {
		return ErrRegress
	}
	d.hello(lo2, hi2)
	e.maxNow = now
	return nil
}

func (d *Device) hello(lo2, hi2 int64) {
	// Every missing sequence number below lo2 is lost, in flight or not.
	removed, _, _ := d.req.RemoveBefore(lo2)
	d.lose(removed)
	removed, rem, hasRem := d.inl.RemoveBefore(lo2)
	d.lose(removed)
	if hasRem {
		// The split remainder stays in flight; its old heap entry is stale
		// because it references the old start.
		heap.Push(&d.inflight, heapEntry{q: rem.Q, start: rem.Start, gen: rem.Gen})
	}
	if hi2 > d.hi {
		start := d.hi + 1
		if start < lo2 {
			// New sequence numbers already below the buffer bound never
			// existed on the device: lost immediately.
			end := lo2 - 1
			if end > hi2 {
				end = hi2
			}
			d.lose([]seqset.Interval{{Start: start, End: end}})
			start = end + 1
		}
		if start <= hi2 {
			d.req.AddMerged(seqset.Interval{Start: start, End: hi2})
		}
		d.hi = hi2
	}
	d.lo = lo2
}

// lose moves the given pieces into the lost set.
func (d *Device) lose(pieces []seqset.Interval) {
	for _, p := range pieces {
		d.lostN += p.Len()
		d.lost.AddMerged(seqset.Interval{Start: p.Start, End: p.End})
	}
}

// SettleTimeouts moves every in-flight segment whose request timed out at
// now back to the requestable set, or to lost when it was already requested
// r times. It returns the number of settled segments. Exported for plan.
func (d *Device) SettleTimeouts(now, tq int64, r int) int {
	settled := 0
	for len(d.inflight) > 0 && d.inflight[0].q+tq <= now {
		ent := heap.Pop(&d.inflight).(heapEntry)
		idx, ok := d.inl.Find(ent.start)
		if !ok {
			continue // stale: segment removed by Ingest/Hello
		}
		iv := d.inl.At(idx)
		if iv.Start != ent.start || iv.Gen != ent.gen {
			continue // stale: segment was split since
		}
		settled++
		d.inl.RemoveAt(idx)
		if iv.C >= r {
			d.lose([]seqset.Interval{{Start: iv.Start, End: iv.End}})
		} else {
			d.req.AddMerged(seqset.Interval{Start: iv.Start, End: iv.End, Attr: seqset.Attr{C: iv.C}})
		}
	}
	return settled
}

// FirstRequestable returns the lowest requestable gap. Exported for plan.
func (d *Device) FirstRequestable() (Gap, bool) {
	iv, ok := d.req.Min()
	if !ok {
		return Gap{}, false
	}
	return Gap{Lo: iv.Start, Hi: iv.End, C: iv.C}, true
}

// IssueRequest moves [a, a+n-1] from the front of the requestable set into
// the in-flight set, stamping it with now and gen. Exported for plan; the
// caller must guarantee the front requestable segment starts at a.
func (d *Device) IssueRequest(a, n int64, c int, now, gen int64) {
	front := d.req.At(0)
	if n == front.Len() {
		d.req.RemoveAt(0)
	} else {
		d.req.ShrinkStart(0, n)
	}
	d.inl.Add(seqset.Interval{
		Start: a,
		End:   a + n - 1,
		Attr:  seqset.Attr{C: c + 1, Inflight: true, Q: now, Gen: gen},
	})
	heap.Push(&d.inflight, heapEntry{q: now, start: a, gen: gen})
}

// NotePlanStats records planning introspection counters for tests.
func (d *Device) NotePlanStats(examined, timeouts int) {
	d.planExamined = examined
	d.planTimeouts = timeouts
}

func (d *Device) stats() Stats {
	f := d.hi
	if iv, ok := d.req.Min(); ok && iv.Start-1 < f {
		f = iv.Start - 1
	}
	if iv, ok := d.inl.Min(); ok && iv.Start-1 < f {
		f = iv.Start - 1
	}
	return Stats{
		Hi:       d.hi,
		Lo:       d.lo,
		F:        f,
		Received: d.received,
		Lost:     d.lostN,
		Missing:  d.req.Total() + d.inl.Total(),
		Dup:      d.dup,
		Late:     d.late,
	}
}

// Stats returns a consistent snapshot of dev's counters.
func (e *Engine) Stats(dev string) (Stats, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.devs[dev]
	if !ok {
		return Stats{}, ErrNoDevice
	}
	return d.stats(), nil
}

// ResetVisited clears the interval-examination counters of dev's sets.
func (e *Engine) ResetVisited(dev string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d, ok := e.devs[dev]; ok {
		d.req.ResetVisited()
		d.inl.ResetVisited()
		d.lost.ResetVisited()
	}
}

// Visited returns how many interval nodes dev's sets examined since the
// last ResetVisited.
func (e *Engine) Visited(dev string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.devs[dev]
	if !ok {
		return 0
	}
	return d.req.Visited() + d.inl.Visited() + d.lost.Visited()
}

// GapSegments returns dev's current number of missing segments (g).
func (e *Engine) GapSegments(dev string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.devs[dev]
	if !ok {
		return 0
	}
	return d.req.Len() + d.inl.Len()
}

// PlanExamined returns the segments examined and timeouts settled by the
// last Plan on dev.
func (e *Engine) PlanExamined(dev string) (examined, timeouts int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.devs[dev]
	if !ok {
		return 0, 0
	}
	return d.planExamined, d.planTimeouts
}
