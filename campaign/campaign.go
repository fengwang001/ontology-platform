// Package campaign orchestrates a firmware OTA upgrade campaign: device
// states, dispatch tokens, per-hop timeouts and the circuit breaker.
//
// Every operation first settles all timeouts with dl <= now in (dl, id)
// order, then validates and executes. A rejected operation changes no
// state: the clock does not advance and the entry settlement is rolled
// back, to be re-applied by the next accepted operation.
package campaign

import (
	"container/heap"
	"errors"
	"fmt"
	"sync"

	"ontology/plan"
	"ontology/slot"
)

// Rejection reasons, checked in the order: ErrInvalid > ErrClockBack >
// ErrUnknown/ErrExists > ErrNotInFlight > ErrStale > ErrVersion > ErrAborted.
var (
	ErrInvalid     = errors.New("campaign: invalid argument")
	ErrClockBack   = errors.New("campaign: clock moved backwards")
	ErrUnknown     = errors.New("campaign: unknown device")
	ErrExists      = errors.New("campaign: device already exists")
	ErrNotInFlight = errors.New("campaign: device not in flight")
	ErrStale       = errors.New("campaign: stale token")
	ErrVersion     = errors.New("campaign: version mismatch")
	ErrAborted     = errors.New("campaign: aborted")
)

const maxClock = int64(1_000_000_000_000)

// State is the lifecycle state of a device.
type State int

const (
	Pending State = iota
	InFlight
	Done
	Failed
	Skipped
	Cancelled
)

func (s State) String() string {
	switch s {
	case Pending:
		return "Pending"
	case InFlight:
		return "InFlight"
	case Done:
		return "Done"
	case Failed:
		return "Failed"
	case Skipped:
		return "Skipped"
	case Cancelled:
		return "Cancelled"
	}
	return "Unknown"
}

// Item is one dispatched device: its id, next-hop version and token.
type Item struct {
	ID  []byte
	Hop int
	Tok uint64
}

type device struct {
	id        string
	ver       int
	state     State
	attempts  int
	readyAt   int64
	hop       int
	dl        int64
	tok       uint64
	readyIdx  int
	flightIdx int
}

// Campaign is the OTA orchestrator. All methods are safe for concurrent
// use; results are equivalent to some serial order.
type Campaign struct {
	mu         sync.Mutex
	plan       *plan.Plan
	slots      *slot.Slot
	maxRetries int
	backoff    int64
	timeout    int64
	breaker    int

	devices  map[string]*device
	ready    readyHeap
	inflight inflightHeap

	maxNow  int64
	tok     uint64
	failed  int
	aborted bool

	popReady  uint64
	popSettle uint64
}

// New builds a campaign: target T, mandatory versions M, in-flight
// capacity C, per-hop attempt limit R, backoff base B, per-hop deadline
// D and circuit-breaker threshold F.
func New(T int, M []int, C, R int, B, D int64, F int) (*Campaign, error) {
	p, err := plan.New(T, M)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	s, err := slot.New(C)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if R < 1 || R > 10 {
		return nil, ErrInvalid
	}
	if B < 1 || B > 1_000_000_000 || D < 1 || D > 1_000_000_000 {
		return nil, ErrInvalid
	}
	if F < 1 || F > 100_000 {
		return nil, ErrInvalid
	}
	return &Campaign{
		plan:       p,
		slots:      s,
		maxRetries: R,
		backoff:    B,
		timeout:    D,
		breaker:    F,
		devices:    make(map[string]*device),
		maxNow:     -1,
	}, nil
}

func validClock(now int64) bool { return now >= 0 && now <= maxClock }

func rollback(undo []func()) {
	for i := len(undo) - 1; i >= 0; i-- {
		undo[i]()
	}
}

// settle applies every timeout with dl <= now in (dl, id) order, recording
// undo closures so a rejected operation can roll the settlement back.
func (c *Campaign) settle(now int64, undo *[]func()) {
	for len(c.inflight) > 0 {
		top := c.inflight[0]
		if top.dl > now {
			return
		}
		c.popSettle++
		c.failLocked(top, top.dl, undo)
	}
}

// failLocked records one failure of an InFlight device at time t.
func (c *Campaign) failLocked(d *device, t int64, undo *[]func()) {
	heap.Remove(&c.inflight, d.flightIdx)
	c.slots.Release()
	prevAttempts, prevReadyAt := d.attempts, d.readyAt
	d.attempts++
	*undo = append(*undo, func() {
		d.state = InFlight
		d.attempts = prevAttempts
		d.readyAt = prevReadyAt
		c.slots.Acquire()
		heap.Push(&c.inflight, d)
	})
	switch {
	case d.attempts >= c.maxRetries:
		d.state = Failed
		c.failed++
		*undo = append(*undo, func() { c.failed-- })
		if !c.aborted && c.failed >= c.breaker {
			c.abortLocked(undo)
		}
	case c.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
		d.readyAt = t + c.backoff*int64(d.attempts)
		heap.Push(&c.ready, d)
		*undo = append(*undo, func() {
			heap.Remove(&c.ready, d.readyIdx)
		})
	}
}

// abortLocked trips the circuit breaker: every Pending device is
// Cancelled; InFlight devices keep running.
func (c *Campaign) abortLocked(undo *[]func()) {
	c.aborted = true
	*undo = append(*undo, func() { c.aborted = false })
	cancelled := make([]*device, 0, len(c.ready))
	for _, d := range c.ready {
		d.state = Cancelled
		d.readyIdx = -1
		cancelled = append(cancelled, d)
	}
	c.ready = c.ready[:0]
	*undo = append(*undo, func() {
		for _, d := range cancelled {
			d.state = Pending
			heap.Push(&c.ready, d)
		}
	})
}

// AddDevice registers a device at version v. Devices with v >= T are
// Skipped; devices added after abort are Cancelled; otherwise the device
// becomes Pending with attempts = 0 and readyAt = now.
func (c *Campaign) AddDevice(now int64, id []byte, v int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(id) == 0 || v < 1 || v > 1_000_000 || !validClock(now) {
		return ErrInvalid
	}
	if now < c.maxNow {
		return ErrClockBack
	}
	var undo []func()
	c.settle(now, &undo)
	key := string(id)
	if _, ok := c.devices[key]; ok {
		rollback(undo)
		return ErrExists
	}
	c.maxNow = now
	d := &device{
		id:        key,
		ver:       v,
		state:     Pending,
		readyAt:   now,
		readyIdx:  -1,
		flightIdx: -1,
	}
	switch {
	case v >= c.plan.Target():
		d.state = Skipped
	case c.aborted:
		d.state = Cancelled
	default:
		heap.Push(&c.ready, d)
	}
	c.devices[key] = d
	return nil
}

// Dispatch moves up to n ready Pending devices (ordered by (readyAt, id),
// bounded by free slots) to InFlight and returns their (id, hop, tok)
// list, possibly empty.
func (c *Campaign) Dispatch(now int64, n int) ([]Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 1 || n > 10_000 || !validClock(now) {
		return nil, ErrInvalid
	}
	if now < c.maxNow {
		return nil, ErrClockBack
	}
	var undo []func()
	c.settle(now, &undo)
	if c.aborted {
		rollback(undo)
		return nil, ErrAborted
	}
	c.maxNow = now
	items := []Item{}
	for len(items) < n && c.slots.Free() > 0 && len(c.ready) > 0 {
		top := c.ready[0]
		if top.readyAt > now {
			break
		}
		heap.Pop(&c.ready)
		c.popReady++
		top.hop = c.plan.Next(top.ver)
		top.dl = now + c.timeout
		c.tok++
		top.tok = c.tok
		top.state = InFlight
		c.slots.Acquire()
		heap.Push(&c.inflight, top)
		items = append(items, Item{ID: []byte(top.id), Hop: top.hop, Tok: top.tok})
	}
	return items, nil
}

// Report applies the outcome of one dispatch. The device must be InFlight
// with a matching token. On success ver must equal the hop; on failure
// ver is ignored.
func (c *Campaign) Report(now int64, id []byte, tok uint64, ver int, ok bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(id) == 0 || tok < 1 || !validClock(now) {
		return ErrInvalid
	}
	if now < c.maxNow {
		return ErrClockBack
	}
	var undo []func()
	c.settle(now, &undo)
	d, exists := c.devices[string(id)]
	switch {
	case !exists:
		rollback(undo)
		return ErrUnknown
	case d.state != InFlight:
		rollback(undo)
		return ErrNotInFlight
	case d.tok != tok:
		rollback(undo)
		return ErrStale
	case ok && ver != d.hop:
		rollback(undo)
		return ErrVersion
	}
	c.maxNow = now
	if !ok {
		c.failLocked(d, now, &undo)
		return nil
	}
	heap.Remove(&c.inflight, d.flightIdx)
	c.slots.Release()
	d.ver = ver
	d.attempts = 0
	switch {
	case ver == c.plan.Target():
		d.state = Done
	case c.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
		d.readyAt = now
		heap.Push(&c.ready, d)
	}
	return nil
}
