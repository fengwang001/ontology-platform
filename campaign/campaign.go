// Package campaign orchestrates a firmware OTA upgrade campaign.
//
// A campaign drives devices hop by hop toward a target version T,
// passing through every mandatory version (package plan), bounded by an
// in-flight slot capacity (package slot). Every operation first settles
// all timeouts with dl <= now in (dl, id) order, then validates and
// executes. Rejected operations change nothing: the clock does not
// advance and the entry settlement is rolled back, so settlement is a
// pure function of now that lands with the next accepted operation.
//
// All methods are safe for concurrent use; the result is equivalent to
// some serial order.
package campaign

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/plan"
	"ontology/slot"
)

// Rejection reasons, checked in the order required by the
// specification: ErrInvalid > ErrClockBack > ErrUnknown/ErrExists >
// ErrNotInFlight > ErrStale > ErrVersion > ErrAborted.
var (
	ErrInvalid     = errors.New("campaign: invalid argument")
	ErrClockBack   = errors.New("campaign: clock moved backwards")
	ErrUnknown     = errors.New("campaign: unknown device")
	ErrExists      = errors.New("campaign: device already exists")
	ErrNotInFlight = errors.New("campaign: device not in flight")
	ErrStale       = errors.New("campaign: stale token")
	ErrVersion     = errors.New("campaign: version mismatch")
	ErrAborted     = errors.New("campaign: activity aborted")
)

const (
	maxClock   = int64(1_000_000_000_000) // 10^12 ms
	maxVersion = 1_000_000
	maxBatch   = 10_000
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

// Counts aggregates device numbers per state.
type Counts struct {
	Pending, InFlight, Done, Failed, Skipped, Cancelled int
}

// Total returns the total number of devices.
func (c Counts) Total() int {
	return c.Pending + c.InFlight + c.Done + c.Failed + c.Skipped + c.Cancelled
}

// Assignment is one dispatched unit of work: device, next hop, token.
type Assignment struct {
	ID  string
	Hop int
	Tok uint64
}

type device struct {
	id       string
	state    State
	version  int
	attempts int
	readyAt  int64
	dl       int64
	hop      int
	tok      uint64
	eidx     int // index inside the expiry heap, valid while InFlight
}

// Campaign is the orchestrator state machine.
type Campaign struct {
	mu      sync.Mutex
	plan    *plan.Plan
	slots   *slot.Counter
	r       int   // per-hop attempt limit
	b       int64 // backoff base, ms
	d       int64 // per-hop deadline, ms
	f       int   // circuit-breaker threshold
	maxNow  int64 // largest accepted now
	tok     uint64
	aborted bool
	failed  int
	devices map[string]*device
	ready   readyHeap
	expiry  expiryHeap

	poppedReady  int // heap pops performed by Dispatch
	poppedExpiry int // heap pops performed by entry settlement
}

// New builds a campaign. T in [2, 10^6]; M holds at most 64 distinct
// positive versions below T; C in [1, 10^4]; R in [1, 10]; B and D in
// [1, 10^9] ms; F in [1, 10^5].
func New(t int, m []int, c, r, b, d, f int) (*Campaign, error) {
	p, err := plan.New(t, m)
	if err != nil {
		return nil, ErrInvalid
	}
	slots, err := slot.New(c)
	if err != nil {
		return nil, ErrInvalid
	}
	if r < 1 || r > 10 {
		return nil, ErrInvalid
	}
	if b < 1 || b > 1_000_000_000 || d < 1 || d > 1_000_000_000 {
		return nil, ErrInvalid
	}
	if f < 1 || f > 100_000 {
		return nil, ErrInvalid
	}
	return &Campaign{
		plan:    p,
		slots:   slots,
		r:       r,
		b:       int64(b),
		d:       int64(d),
		f:       f,
		devices: make(map[string]*device),
	}, nil
}

// AddDevice registers a device at version v. Devices with v >= T are
// Skipped; devices added after abort are Cancelled; otherwise the
// device becomes Pending with attempts=0 and readyAt=now.
func (c *Campaign) AddDevice(now int64, id string, v int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < 0 || now > maxClock || id == "" || v < 1 || v > maxVersion {
		return ErrInvalid
	}
	if now < c.maxNow {
		return ErrClockBack
	}
	u := c.settle(now)
	if _, dup := c.devices[id]; dup {
		u.rollback(c)
		return ErrExists
	}
	dev := &device{id: id, version: v, readyAt: now}
	switch {
	case v >= c.plan.Target():
		dev.state = Skipped
	case c.aborted:
		dev.state = Cancelled
	default:
		dev.state = Pending
		heap.Push(&c.ready, dev)
	}
	c.devices[id] = dev
	c.maxNow = now
	return nil
}

// Dispatch moves up to n ready devices (readyAt <= now, ordered by
// (readyAt, id)) to InFlight, bounded by free slots, and returns the
// (id, hop, tok) assignments in dispatch order.
func (c *Campaign) Dispatch(now int64, n int) ([]Assignment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < 0 || now > maxClock || n < 1 || n > maxBatch {
		return nil, ErrInvalid
	}
	if now < c.maxNow {
		return nil, ErrClockBack
	}
	u := c.settle(now)
	if c.aborted {
		u.rollback(c)
		return nil, ErrAborted
	}
	k := min(n, c.slots.Free())
	var out []Assignment
	for len(out) < k && len(c.ready) > 0 && c.ready[0].readyAt <= now {
		dev := heap.Pop(&c.ready).(*device)
		c.poppedReady++
		c.slots.Acquire()
		c.tok++
		dev.state = InFlight
		dev.hop = c.plan.Next(dev.version)
		dev.dl = now + c.d
		dev.tok = c.tok
		heap.Push(&c.expiry, dev)
		out = append(out, Assignment{ID: dev.id, Hop: dev.hop, Tok: dev.tok})
	}
	c.maxNow = now
	return out, nil
}

// Report records the outcome of a dispatched hop. The device must be
// InFlight and tok must match its current token. On success ver must
// equal the assigned hop.
func (c *Campaign) Report(now int64, id string, tok uint64, ver int, ok bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < 0 || now > maxClock {
		return ErrInvalid
	}
	if now < c.maxNow {
		return ErrClockBack
	}
	u := c.settle(now)
	dev, known := c.devices[id]
	if !known {
		u.rollback(c)
		return ErrUnknown
	}
	if dev.state != InFlight {
		u.rollback(c)
		return ErrNotInFlight
	}
	if dev.tok != tok {
		u.rollback(c)
		return ErrStale
	}
	if ok && ver != dev.hop {
		u.rollback(c)
		return ErrVersion
	}
	heap.Remove(&c.expiry, dev.eidx)
	if ok {
		dev.version = ver
		dev.attempts = 0
		c.slots.Release()
		switch {
		case ver == c.plan.Target():
			dev.state = Done
		case c.aborted:
			dev.state = Cancelled
		default:
			dev.state = Pending
			dev.readyAt = now
			heap.Push(&c.ready, dev)
		}
	} else {
		c.fail(dev, now, nil)
	}
	c.maxNow = now
	return nil
}

// Counts returns the per-state device counts.
func (c *Campaign) Counts() Counts {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n Counts
	for _, dev := range c.devices {
		switch dev.state {
		case Pending:
			n.Pending++
		case InFlight:
			n.InFlight++
		case Done:
			n.Done++
		case Failed:
			n.Failed++
		case Skipped:
			n.Skipped++
		case Cancelled:
			n.Cancelled++
		}
	}
	return n
}

// Aborted reports whether the circuit breaker has fired.
func (c *Campaign) Aborted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted
}

// StateOf returns the state of a device and whether it exists.
func (c *Campaign) StateOf(id string) (State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dev, ok := c.devices[id]
	if !ok {
		return 0, false
	}
	return dev.state, true
}

// fail applies one failed attempt to an InFlight device at the given
// failure time (dl for timeouts, now for negative reports). The device
// must already be out of the expiry heap.
func (c *Campaign) fail(dev *device, at int64, u *settleUndo) {
	if u != nil {
		u.backup(dev)
	}
	dev.attempts++
	c.slots.Release()
	switch {
	case dev.attempts >= c.r:
		dev.state = Failed
		c.failed++
		if c.failed >= c.f && !c.aborted {
			c.abort(u)
		}
	case c.aborted:
		dev.state = Cancelled
	default:
		dev.state = Pending
		dev.readyAt = at + c.b*int64(dev.attempts)
		heap.Push(&c.ready, dev)
	}
}

// abort trips the circuit breaker: every Pending device is Cancelled,
// in-flight devices keep running.
func (c *Campaign) abort(u *settleUndo) {
	c.aborted = true
	for _, dev := range c.ready {
		if u != nil {
			u.backup(dev)
		}
		dev.state = Cancelled
	}
	c.ready = nil
}

// settleUndo captures everything settle may mutate so a rejected
// operation can restore the exact pre-settlement state.
type settleUndo struct {
	snapped      bool
	ready        readyHeap
	expiry       expiryHeap
	used         int
	failed       int
	aborted      bool
	poppedExpiry int
	seen         map[*device]struct{}
	devs         []deviceBackup
}

type deviceBackup struct {
	dev      *device
	state    State
	attempts int
	readyAt  int64
}

func (u *settleUndo) backup(dev *device) {
	if u.seen == nil {
		u.seen = make(map[*device]struct{})
	}
	if _, ok := u.seen[dev]; ok {
		return
	}
	u.seen[dev] = struct{}{}
	u.devs = append(u.devs, deviceBackup{
		dev: dev, state: dev.state, attempts: dev.attempts, readyAt: dev.readyAt,
	})
}

func (u *settleUndo) rollback(c *Campaign) {
	if u.snapped {
		c.ready = u.ready
		c.expiry = u.expiry
		for i, dev := range c.expiry {
			dev.eidx = i
		}
	}
	for i := len(u.devs) - 1; i >= 0; i-- {
		b := u.devs[i]
		b.dev.state = b.state
		b.dev.attempts = b.attempts
		b.dev.readyAt = b.readyAt
	}
	for c.slots.Used() < u.used {
		c.slots.Acquire()
	}
	c.failed = u.failed
	c.aborted = u.aborted
	c.poppedExpiry = u.poppedExpiry
}

// settle processes every timeout with dl <= now in (dl, id) order and
// returns the undo log for a potential rejection rollback.
func (c *Campaign) settle(now int64) *settleUndo {
	u := &settleUndo{
		used:         c.slots.Used(),
		failed:       c.failed,
		aborted:      c.aborted,
		poppedExpiry: c.poppedExpiry,
	}
	for len(c.expiry) > 0 && c.expiry[0].dl <= now {
		if !u.snapped {
			u.ready = append(readyHeap(nil), c.ready...)
			u.expiry = append(expiryHeap(nil), c.expiry...)
			u.snapped = true
		}
		dev := heap.Pop(&c.expiry).(*device)
		c.poppedExpiry++
		c.fail(dev, dev.dl, u)
	}
	return u
}

// readyHeap orders Pending devices by (readyAt, id); string comparison
// is bytewise, matching the required byte order.
type readyHeap []*device

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].readyAt != h[j].readyAt {
		return h[i].readyAt < h[j].readyAt
	}
	return h[i].id < h[j].id
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(*device)) }
func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// expiryHeap orders InFlight devices by (dl, id) and tracks each
// device's index so reports can remove it in O(log n).
type expiryHeap []*device

func (h expiryHeap) Len() int { return len(h) }
func (h expiryHeap) Less(i, j int) bool {
	if h[i].dl != h[j].dl {
		return h[i].dl < h[j].dl
	}
	return h[i].id < h[j].id
}
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].eidx = i
	h[j].eidx = j
}
func (h *expiryHeap) Push(x any) {
	dev := x.(*device)
	dev.eidx = len(*h)
	*h = append(*h, dev)
}
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}
