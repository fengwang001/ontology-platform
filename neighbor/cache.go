// Package neighbor implements an IPv6 NDP-style neighbor cache state machine.
//
// Each neighbor address may be in one of five states: Incomplete, Reachable,
// Stale, Delay or Probe. Packets sent to an unresolved neighbor are queued
// until an advertisement supplies a link-layer address; they are then
// delivered in FIFO order. Time is injected by callers so that replaying the
// same operation sequence always produces the same result.
//
// All exported operations are safe for concurrent use. Every operation first
// performs expiry processing and then at most one action per entry.
package neighbor

import (
	"errors"
	"log"
	"sort"
	"strconv"
	"sync"
)

// State of a neighbor cache entry.
type State uint8

const (
	Incomplete State = iota
	Reachable
	Stale
	Delay
	Probe
)

func (s State) String() string {
	switch s {
	case Incomplete:
		return "INCOMPLETE"
	case Reachable:
		return "REACHABLE"
	case Stale:
		return "STALE"
	case Delay:
		return "DELAY"
	case Probe:
		return "PROBE"
	default:
		return "UNKNOWN"
	}
}

// Time is an abstract, monotonically supplied timestamp (implementation unit,
// typically nanoseconds). Only ordering matters to the cache.
type Time int64

// Packet is an opaque outbound payload addressed to a neighbor.
type Packet any

// Addr is a network-layer neighbor address.
type Addr string

// LinkAddr is a link-layer address.
type LinkAddr string

// Delivery is a packet released toward a resolved neighbor.
type Delivery struct {
	Addr   Addr
	Link   LinkAddr
	Packet Packet
}

// Expiry describes one state transition performed by expiry processing.
type Expiry struct {
	Addr    Addr
	From    State
	To      State
	At      Time
	Request bool // a probe/request was (re)sent due to this expiry
	Deleted bool // the entry was deleted after exhausting retries
}

// Result is the deterministic outcome of one operation, including the expiry
// work performed at the beginning of the operation.
type Result struct {
	// Requests are address-resolution requests that must be sent, in order.
	Requests []Addr
	// Delivered are packets released toward a resolved neighbor, in FIFO order.
	Delivered []Delivery
	// DroppedUnreachable are queued packets discarded because resolution failed.
	DroppedUnreachable []Packet
	// DroppedOverflow are packets evicted from a full per-entry queue.
	DroppedOverflow []Packet
	// Expired reports every transition performed by expiry processing.
	Expired []Expiry
}

// Sentinel rejection errors. Validation reports only the first, in the fixed
// order documented on the operation methods.
var (
	ErrClockMovedBackward = errors.New("neighbor: current time is earlier than the last processed time")
	ErrEmptyAddr          = errors.New("neighbor: neighbor address is empty")
	ErrEmptyLinkAddr      = errors.New("neighbor: link-layer address is empty")
	ErrNoEntry            = errors.New("neighbor: no entry for neighbor address")
	ErrCacheFull          = errors.New("neighbor: cache entry limit reached")
)

// Config holds the timing and capacity parameters.
type Config struct {
	// ReachableTime R: a Reachable entry expires to Stale at now+R.
	ReachableTime Time
	// DelayTime Dl: a Stale entry that saw a send enters Delay; it expires to
	// Probe after Dl.
	DelayTime Time
	// Retransmit T is the spacing between resolution requests/probes.
	Retransmit Time
	// MaxSends K is the maximum requests/probes before an Incomplete or Probe
	// entry is deleted.
	MaxSends int
	// QueueLimit Q is the maximum queued packets per Incomplete entry; an
	// overflow evicts the oldest packet.
	QueueLimit int
	// MaxEntries limits the number of entries; zero means unlimited.
	MaxEntries int
}

// Cache is a concurrency-safe neighbor cache.
type Cache struct {
	mu       sync.Mutex
	cfg      Config
	log      *log.Logger
	seq      uint64
	last     Time
	haveLast bool
	entries  map[Addr]*entry
}

type entry struct {
	state    State
	link     LinkAddr
	deadline Time // next expiry/retransmit time; 0 means no timer
	sent     int
	queue    []Packet
}

// New creates a cache with the given configuration and diagnostic logger
// (nil disables logging).
func New(cfg Config, logger *log.Logger) *Cache {
	return &Cache{cfg: cfg, log: logger, entries: make(map[Addr]*entry)}
}

func (c *Cache) logf(format string, args ...any) {
	if c.log != nil {
		c.log.Printf(format, args...)
	}
}

func boolStr(b bool) string { return strconv.FormatBool(b) }

// begin serializes an operation, checks the clock and runs expiry processing.
func (c *Cache) begin(now Time, op string, detail string) (Result, uint64, error) {
	c.seq++
	seq := c.seq
	c.logf("#%d %s now=%d %s [input]", seq, op, now, detail)
	if c.haveLast && now < c.last {
		c.logf("#%d reject %v [basis: clock moved backward %d -> %d]", seq, ErrClockMovedBackward, c.last, now)
		return Result{}, seq, ErrClockMovedBackward
	}
	res := c.expire(now, seq)
	c.last = now
	c.haveLast = true
	return res, seq, nil
}

func finish(c *Cache, seq uint64, op string, res *Result) Result {
	c.logf("#%d %s -> requests=%d delivered=%d dropUnreachable=%d dropOverflow=%d expired=%d [output]",
		seq, op, len(res.Requests), len(res.Delivered), len(res.DroppedUnreachable), len(res.DroppedOverflow), len(res.Expired))
	return *res
}

// expire performs at most one due action per entry. Entries are visited in
// sorted address order so the same input sequence yields the same side-effect
// order. "Now not earlier than deadline" (now >= deadline) is the boundary.
func (c *Cache) expire(now Time, seq uint64) Result {
	var res Result
	addrs := make([]Addr, 0, len(c.entries))
	for a := range c.entries {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	for _, a := range addrs {
		e := c.entries[a]
		if e.deadline == 0 || now < e.deadline {
			continue
		}
		switch e.state {
		case Reachable:
			e.state = Stale
			e.deadline = 0
			res.Expired = append(res.Expired, Expiry{Addr: a, From: Reachable, To: Stale, At: now})
			c.logf("#%d expire addr=%s REACHABLE due at now=%d -> STALE [basis: reachable time R elapsed]", seq, a, now)
		case Delay:
			e.state = Probe
			e.sent = 1
			e.deadline = now + c.cfg.Retransmit
			res.Requests = append(res.Requests, a)
			res.Expired = append(res.Expired, Expiry{Addr: a, From: Delay, To: Probe, At: now, Request: true})
			c.logf("#%d expire addr=%s DELAY due at now=%d -> PROBE, send probe #1, next retransmit=%d [basis: delay time Dl elapsed]", seq, a, now, e.deadline)
		case Incomplete, Probe:
			from := e.state
			if e.sent < c.cfg.MaxSends {
				e.sent++
				old := e.deadline
				e.deadline += c.cfg.Retransmit
				res.Requests = append(res.Requests, a)
				res.Expired = append(res.Expired, Expiry{Addr: a, From: from, To: from, At: now, Request: true})
				c.logf("#%d expire addr=%s %s retransmit due at %d, sends=%d<%d -> send #%d, next=%d [basis: retransmit timer T]", seq, a, from, old, e.sent-1, c.cfg.MaxSends, e.sent, e.deadline)
			} else {
				delete(c.entries, a)
				res.Expired = append(res.Expired, Expiry{Addr: a, From: from, To: from, At: now, Deleted: true})
				c.logf("#%d expire addr=%s %s sends=%d>=K=%d -> DELETE [basis: max sends exhausted]", seq, a, from, e.sent, c.cfg.MaxSends)
				if from == Incomplete {
					res.DroppedUnreachable = append(res.DroppedUnreachable, e.queue...)
					c.logf("#%d expire addr=%s discard %d queued packet(s) as unreachable [basis: incomplete entry deleted]", seq, a, len(e.queue))
				}
			}
		}
	}
	return res
}

// enqueue appends pkt to e's queue, evicting the oldest packet when the queue
// already holds QueueLimit packets.
func (c *Cache) enqueue(e *entry, pkt Packet, res *Result) {
	if c.cfg.QueueLimit > 0 && len(e.queue) >= c.cfg.QueueLimit {
		oldest := e.queue[0]
		e.queue = e.queue[1:]
		res.DroppedOverflow = append(res.DroppedOverflow, oldest)
		c.logf("queue overflow: drop oldest packet=%v [basis: queue limit Q=%d reached]", oldest, c.cfg.QueueLimit)
	}
	e.queue = append(e.queue, pkt)
}

// stateOf and deadlineOf expose internal state for white-box testing.
func (c *Cache) stateOf(addr Addr) (State, bool) {
	e, ok := c.entries[addr]
	if !ok {
		return 0, false
	}
	return e.state, true
}

func (c *Cache) linkOf(addr Addr) LinkAddr {
	if e, ok := c.entries[addr]; ok {
		return e.link
	}
	return ""
}

func (c *Cache) queuedOf(addr Addr) int {
	if e, ok := c.entries[addr]; ok {
		return len(e.queue)
	}
	return 0
}

// Len reports the current number of entries (mainly for tests).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
