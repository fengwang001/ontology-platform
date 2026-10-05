// Package campaign orchestrates a firmware OTA upgrade campaign:
// it dispatches devices hop by hop through the mandatory versions to
// the target version, bounded by in-flight slots, per-hop attempt
// limits, backoff, deadlines and a failure circuit breaker.
//
// All methods are safe for concurrent use; the result is equivalent
// to some serial order. Every operation first settles the timeouts
// with dl <= now in (dl, id) order. The settlement is a pure function
// of now: a rejected operation rolls it back together with every
// other state change, so a later accepted operation at the same now
// replays it identically.
package campaign

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/plan"
	"ontology/slot"
)

// Rejection errors, checked in the order listed below; only the first
// matching one is reported and a rejected operation changes nothing.
var (
	ErrInvalid     = errors.New("campaign: invalid argument")
	ErrClockBack   = errors.New("campaign: clock moved backwards")
	ErrUnknown     = errors.New("campaign: unknown device")
	ErrExists      = errors.New("campaign: device already exists")
	ErrNotInFlight = errors.New("campaign: device not in flight")
	ErrStale       = errors.New("campaign: stale token")
	ErrVersion     = errors.New("campaign: version mismatch")
	ErrAborted     = errors.New("campaign: campaign aborted")
)

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

var stateNames = []string{"Pending", "InFlight", "Done", "Failed", "Skipped", "Cancelled"}

func (s State) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return "Unknown"
	}
	return stateNames[s]
}

const maxClock = 1_000_000_000_000

// Item is one dispatched assignment: device id, next-hop version and
// the campaign-wide monotonically increasing token.
type Item struct {
	ID  []byte
	Hop int
	Tok int
}

type device struct {
	id       string
	state    State
	ver      int
	attempts int
	readyAt  int64
	hop      int
	tok      int
	dl       int64
}

// readyHeap holds Pending devices ordered by (readyAt, id).
type readyHeap []*device

func (h readyHeap) Len() int { return len(h) }

func (h readyHeap) Less(i, j int) bool {
	if h[i].readyAt != h[j].readyAt {
		return h[i].readyAt < h[j].readyAt
	}
	return h[i].id < h[j].id
}

func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *readyHeap) Push(x any) { *h = append(*h, x.(*device)) }

func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return d
}

// Campaign is the OTA upgrade orchestrator.
type Campaign struct {
	mu      sync.Mutex
	plan    *plan.Plan
	slots   *slot.Slot
	r       int // per-hop attempt limit
	b       int64
	d       int64
	f       int // circuit-breaker threshold
	devices map[string]*device
	ready   readyHeap
	aborted bool
	failed  int
	tok     int
	clock   int64
	popped  int // heap pops performed by accepted operations
}

// New validates the parameters and returns an empty campaign.
func New(T int, M []int, C, R int, B, D int64, F int) (*Campaign, error) {
	p, err := plan.New(T, M)
	if err != nil {
		return nil, ErrInvalid
	}
	if C < 1 || C > 10_000 || R < 1 || R > 10 ||
		B < 1 || B > 1_000_000_000 || D < 1 || D > 1_000_000_000 ||
		F < 1 || F > 100_000 {
		return nil, ErrInvalid
	}
	return &Campaign{
		plan:    p,
		slots:   slot.New(C),
		r:       R,
		b:       B,
		d:       D,
		f:       F,
		devices: make(map[string]*device),
		clock:   -1,
	}, nil
}

// transition is the computed effect of settling one timed-out device.
type transition struct {
	dev      *device
	state    State
	attempts int
	readyAt  int64
}

// settlement is the tentative result of settling timeouts at a given
// now. It is a pure function of (state, now): entries are popped from
// the slot structure but device states are untouched until commit;
// rollback puts the entries back.
type settlement struct {
	entries   []slot.Entry
	trans     []transition
	post      map[*device]State
	abortNow  bool
	failedAdd int
}

func (s *settlement) stateOf(d *device) State {
	if st, ok := s.post[d]; ok {
		return st
	}
	return d.state
}

func (s *settlement) aborted(base bool) bool { return base || s.abortNow }

// enter validates now and computes the tentative settlement.
func (c *Campaign) enter(now int64) (*settlement, error) {
	if now < 0 || now > maxClock {
		return nil, ErrInvalid
	}
	if now < c.clock {
		return nil, ErrClockBack
	}
	return c.settle(now), nil
}

// settle pops every in-flight device with dl <= now in (dl, id) order
// and computes its transition, including a mid-settlement circuit
// break: timeouts after the break still settle, but land in Cancelled
// instead of Pending.
func (c *Campaign) settle(now int64) *settlement {
	s := &settlement{
		entries: c.slots.Expire(now),
		post:    make(map[*device]State),
	}
	aborted := c.aborted
	failed := c.failed
	for _, e := range s.entries {
		d := c.devices[e.ID]
		att := d.attempts + 1
		tr := transition{dev: d, attempts: att}
		switch {
		case att >= c.r:
			tr.state = Failed
			failed++
			s.failedAdd++
			if !aborted && failed >= c.f {
				aborted = true
				s.abortNow = true
			}
		case aborted:
			tr.state = Cancelled
		default:
			tr.state = Pending
			tr.readyAt = e.DL + c.b*int64(att)
		}
		s.trans = append(s.trans, tr)
		s.post[d] = tr.state
	}
	return s
}

// rollback undoes a tentative settlement: the popped slot entries go
// back and device states were never touched.
func (c *Campaign) rollback(s *settlement) {
	for _, e := range s.entries {
		c.slots.Restore(e)
	}
}

// commit applies a settlement for good.
func (c *Campaign) commit(s *settlement) {
	for _, tr := range s.trans {
		d := tr.dev
		d.state = tr.state
		d.attempts = tr.attempts
		if tr.state == Pending {
			d.readyAt = tr.readyAt
			heap.Push(&c.ready, d)
		}
	}
	c.failed += s.failedAdd
	c.popped += len(s.entries)
	if s.abortNow {
		c.abort()
	}
}

// abort trips the circuit breaker: every Pending device is Cancelled,
// in-flight devices keep running.
func (c *Campaign) abort() {
	c.aborted = true
	for _, d := range c.devices {
		if d.state == Pending {
			d.state = Cancelled
		}
	}
	c.ready = nil
}

// AddDevice registers a device. A device already at or above the
// target is Skipped; one added after the abort is Cancelled; otherwise
// it becomes Pending with attempts=0 and readyAt=now.
func (c *Campaign) AddDevice(now int64, id []byte, v int) error {
	if len(id) == 0 || v < 1 || v > 1_000_000 {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.enter(now)
	if err != nil {
		return err
	}
	key := string(id)
	if _, dup := c.devices[key]; dup {
		c.rollback(s)
		return ErrExists
	}
	c.commit(s)
	d := &device{id: key, ver: v, readyAt: now}
	switch {
	case v >= c.plan.Target():
		d.state = Skipped
	case c.aborted:
		d.state = Cancelled
	default:
		d.state = Pending
		heap.Push(&c.ready, d)
	}
	c.devices[key] = d
	c.clock = now
	return nil
}

// Dispatch moves up to n ready Pending devices (ordered by readyAt,
// then id) to InFlight, bounded by the free slots, and returns the
// (id, hop, tok) assignments. The list may be empty.
func (c *Campaign) Dispatch(now int64, n int) ([]Item, error) {
	if n < 1 || n > 10_000 {
		return nil, ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.enter(now)
	if err != nil {
		return nil, err
	}
	if s.aborted(c.aborted) {
		c.rollback(s)
		return nil, ErrAborted
	}
	c.commit(s)
	k := min(n, c.slots.Free())
	var out []Item
	for len(out) < k && len(c.ready) > 0 {
		top := c.ready[0]
		if top.readyAt > now {
			break
		}
		heap.Pop(&c.ready)
		c.popped++
		c.tok++
		top.state = InFlight
		top.hop = c.plan.Next(top.ver)
		top.tok = c.tok
		top.dl = now + c.d
		c.slots.Acquire(top.id, top.dl)
		out = append(out, Item{ID: []byte(top.id), Hop: top.hop, Tok: top.tok})
	}
	c.clock = now
	return out, nil
}

// Report records the outcome of one in-flight hop. On success ver
// must equal the assigned hop; on failure ver is ignored.
func (c *Campaign) Report(now int64, id []byte, tok, ver int, ok bool) error {
	if len(id) == 0 {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.enter(now)
	if err != nil {
		return err
	}
	d, found := c.devices[string(id)]
	if !found {
		c.rollback(s)
		return ErrUnknown
	}
	if s.stateOf(d) != InFlight {
		c.rollback(s)
		return ErrNotInFlight
	}
	if tok != d.tok {
		c.rollback(s)
		return ErrStale
	}
	if ok && ver != d.hop {
		c.rollback(s)
		return ErrVersion
	}
	c.commit(s)
	c.slots.Release(d.id)
	if ok {
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
	} else {
		d.attempts++
		switch {
		case d.attempts >= c.r:
			d.state = Failed
			c.failed++
			if !c.aborted && c.failed >= c.f {
				c.abort()
			}
		case c.aborted:
			d.state = Cancelled
		default:
			d.state = Pending
			d.readyAt = now + c.b*int64(d.attempts)
			heap.Push(&c.ready, d)
		}
	}
	c.clock = now
	return nil
}
