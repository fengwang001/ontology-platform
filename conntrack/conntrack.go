// Package conntrack implements a source-NAT (outbound) connection tracking
// table that hands out external ports, enforces per-state timeouts and only
// admits inbound packets belonging to a tracked connection.
package conntrack

import (
	"container/heap"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"sync"
)

// Rejection causes. Each rejected operation returns the first applicable error
// and leaves every connection and expiry untouched.
var (
	// ErrInvalidConfig wraps constructor failures (bad port range, N or timeouts).
	ErrInvalidConfig = errors.New("conntrack: invalid configuration")
	// ErrClockSkew: the event timestamp is earlier than the table's clock.
	ErrClockSkew = errors.New("conntrack: clock moved backwards")
	// ErrNotSynNoConn: outbound packet finds no connection and is not a SYN.
	ErrNotSynNoConn = errors.New("conntrack: no matching connection and packet is not SYN")
	// ErrTableFull: a new connection is needed but N connections already exist.
	ErrTableFull = errors.New("conntrack: connection table full")
	// ErrPortPoolExhausted: a new internal endpoint needs a port but the pool is empty.
	ErrPortPoolExhausted = errors.New("conntrack: external port pool exhausted")
	// ErrNoMapping: inbound packet arrives on an external port nobody owns.
	ErrNoMapping = errors.New("conntrack: external port has no mapping")
	// ErrNoConnectionForRemote: the owner has no connection to that remote endpoint.
	ErrNoConnectionForRemote = errors.New("conntrack: no connection to that remote endpoint")
)

// ConnState is the lifecycle state of a tracked connection.
type ConnState int

const (
	StateHalfOpen ConnState = iota
	StateEstablished
	StateClosed
)

// EventType identifies the TCP-like event carried by a packet.
type EventType int

const (
	EventSYN EventType = iota
	EventDATA
	EventFIN
	EventRST
)

// Direction says whether a packet leaves or enters the internal network.
type Direction int

const (
	DirOutbound Direction = iota
	DirInbound
)

// Endpoint is an IP address / port pair.
type Endpoint struct {
	Addr netip.Addr
	Port uint16
}

// Packet is one timestamped event.
type Packet struct {
	Now      int64
	Internal Endpoint
	Remote   Endpoint
	Event    EventType
}

// ConnInfo describes one live tracked connection.
type ConnInfo struct {
	Internal     Endpoint
	Remote       Endpoint
	ExternalPort uint16
	State        ConnState
	Expiry       int64
}

// Result is the verdict for a processed packet.
type Result struct {
	Allowed      bool
	ExternalPort uint16
	State        ConnState
	Expiry       int64
}

// Logger receives structured decision lines.
type Logger interface {
	Printf(format string, args ...any)
}

// Option customizes a Table.
type Option func(*config)

type config struct {
	logger Logger
}

// WithLogger sets the decision logger (defaults to the standard logger).
func WithLogger(l Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

type endpointInfo struct {
	port uint16
	refs int
}

type connKey struct {
	internal Endpoint
	remote   Endpoint
}

type conn struct {
	key    connKey
	port   uint16
	state  ConnState
	expiry int64
}

// Table is the concurrent-safe connection tracking table.
type Table struct {
	mu sync.RWMutex

	lo, hi uint16
	max    int
	th     int64
	te     int64
	tc     int64

	free    portHeap
	epExt   map[Endpoint]*endpointInfo
	extEP   map[uint16]Endpoint
	conns   map[connKey]*conn
	lastNow int64
	log     Logger
}

// New constructs a table with the closed port pool [lo,hi], at most maxConns
// live connections and half-open/established/closed timeouts th/te/tc.
func New(lo, hi uint16, maxConns int, th, te, tc int64, opts ...Option) (*Table, error) {
	switch {
	case lo > hi:
		return nil, fmt.Errorf("%w: lo (%d) > hi (%d)", ErrInvalidConfig, lo, hi)
	case maxConns <= 0:
		return nil, fmt.Errorf("%w: max connections must be positive, got %d", ErrInvalidConfig, maxConns)
	case th <= 0 || te <= 0 || tc <= 0:
		return nil, fmt.Errorf("%w: timeouts must be positive, got th=%d te=%d tc=%d", ErrInvalidConfig, th, te, tc)
	}

	cfg := config{logger: log.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}

	free := make(portHeap, 0, int(hi)-int(lo)+1)
	for port := lo; ; port++ {
		free = append(free, port)
		if port == hi {
			break
		}
	}
	heap.Init(&free)

	return &Table{
		lo:    lo,
		hi:    hi,
		max:   maxConns,
		th:    th,
		te:    te,
		tc:    tc,
		free:  free,
		epExt: make(map[Endpoint]*endpointInfo),
		extEP: make(map[uint16]Endpoint),
		conns: make(map[connKey]*conn),
		log:   cfg.logger,
	}, nil
}

// HandleOutbound processes an outbound packet. Only an outbound SYN may create
// a connection.
func (t *Table) HandleOutbound(p Packet) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.logInput(DirOutbound, p, 0)
	if err := t.advanceClock(p.Now); err != nil {
		return Result{}, err
	}
	t.sweep(p.Now)

	key := connKey{internal: p.Internal, remote: p.Remote}
	c, ok := t.conns[key]
	if !ok {
		if p.Event != EventSYN {
			return t.reject("no connection %s -> %s and event %s is not SYN", ErrNotSynNoConn,
				p.Internal, p.Remote, eventName(p.Event))
		}
		if len(t.conns) >= t.max {
			return t.reject("table full at %d/%d connections", ErrTableFull, len(t.conns), t.max)
		}

		ep, hasEP := t.epExt[p.Internal]
		if !hasEP {
			if t.free.Len() == 0 {
				return t.reject("new endpoint %s needs a port but pool [%d,%d] is empty", ErrPortPoolExhausted,
					p.Internal, t.lo, t.hi)
			}
			port := heap.Pop(&t.free).(uint16)
			ep = &endpointInfo{port: port}
			t.epExt[p.Internal] = ep
			t.extEP[port] = p.Internal
			t.logPrintf("now=%d ALLOCATE endpoint=%s gets external port=%d (smallest free)",
				p.Now, p.Internal, port)
		}

		c = &conn{key: key, port: ep.port, state: StateHalfOpen, expiry: p.Now + t.th}
		t.conns[key] = c
		ep.refs++
		t.logPrintf("now=%d CREATE %s -> %s external=%d state=half-open expiry=%d (+th=%d)",
			p.Now, p.Internal, p.Remote, ep.port, c.expiry, t.th)
		return t.allow(c), nil
	}

	return t.apply(p.Now, c, p.Event, DirOutbound)
}

// HandleInbound processes an inbound packet arriving on externalPort. The
// owning internal endpoint is derived from the port mapping, so p.Internal is
// ignored.
func (t *Table) HandleInbound(p Packet, externalPort uint16) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.logInput(DirInbound, p, externalPort)
	if err := t.advanceClock(p.Now); err != nil {
		return Result{}, err
	}
	t.sweep(p.Now)

	internal, ok := t.extEP[externalPort]
	if !ok {
		return t.reject("external port=%d has no endpoint mapping", ErrNoMapping, externalPort)
	}
	c, ok := t.conns[connKey{internal: internal, remote: p.Remote}]
	if !ok {
		return t.reject("port=%d owned by %s but no connection to remote=%s", ErrNoConnectionForRemote,
			externalPort, internal, p.Remote)
	}

	return t.apply(p.Now, c, p.Event, DirInbound)
}

// apply runs the state machine for an existing connection. It must be called
// with the write lock held and after expired connections have been swept.
func (t *Table) apply(now int64, c *conn, ev EventType, dir Direction) (Result, error) {
	oldState := c.state

	switch ev {
	case EventRST:
		t.deleteConn(c)
		delete(t.conns, c.key)
		t.logPrintf("now=%d DELETE %s -> %s external=%d reason=RST",
			now, c.key.internal, c.key.remote, c.port)
		return Result{Allowed: true, ExternalPort: c.port, State: oldState, Expiry: c.expiry}, nil

	case EventFIN:
		if c.state != StateClosed {
			c.state = StateClosed
			c.expiry = now + t.tc
			t.logPrintf("now=%d FIN %s -> %s external=%d %s->closed expiry=%d (+tc=%d)",
				now, c.key.internal, c.key.remote, c.port, stateName(oldState), c.expiry, t.tc)
		} else {
			t.logPrintf("now=%d FIN %s -> %s external=%d already closed, expiry=%d unchanged",
				now, c.key.internal, c.key.remote, c.port, c.expiry)
		}

	default: // SYN or DATA
		switch c.state {
		case StateHalfOpen:
			if dir == DirInbound {
				c.state = StateEstablished
				c.expiry = now + t.te
				t.logPrintf("now=%d inbound %s %s -> %s external=%d half-open->established expiry=%d (+te=%d)",
					now, eventName(ev), c.key.internal, c.key.remote, c.port, c.expiry, t.te)
			} else {
				c.expiry = now + t.th
				t.logPrintf("now=%d outbound %s %s -> %s external=%d refresh half-open expiry=%d (+th=%d)",
					now, eventName(ev), c.key.internal, c.key.remote, c.port, c.expiry, t.th)
			}
		case StateEstablished:
			c.expiry = now + t.te
			t.logPrintf("now=%d %s %s %s -> %s external=%d refresh established expiry=%d (+te=%d)",
				now, dirName(dir), eventName(ev), c.key.internal, c.key.remote, c.port, c.expiry, t.te)
		case StateClosed:
			t.logPrintf("now=%d %s %s %s -> %s external=%d closed, expiry=%d unchanged",
				now, dirName(dir), eventName(ev), c.key.internal, c.key.remote, c.port, c.expiry)
		}
	}

	return t.allow(c), nil
}

// Snapshot returns all connections alive at time now (expiry strictly greater
// than now), sorted deterministically. It never mutates table state.
func (t *Table) Snapshot(now int64) []ConnInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	live := make([]ConnInfo, 0, len(t.conns))
	for _, c := range t.conns {
		if c.expiry > now {
			live = append(live, ConnInfo{
				Internal:     c.key.internal,
				Remote:       c.key.remote,
				ExternalPort: c.port,
				State:        c.state,
				Expiry:       c.expiry,
			})
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].Internal != live[j].Internal {
			return endpointLess(live[i].Internal, live[j].Internal)
		}
		return endpointLess(live[i].Remote, live[j].Remote)
	})
	return live
}

// advanceClock rejects events older than the latest accepted timestamp.
func (t *Table) advanceClock(now int64) error {
	if now < t.lastNow {
		t.logPrintf("now=%d REJECT clock skew: now < last=%d", now, t.lastNow)
		return ErrClockSkew
	}
	t.lastNow = now
	return nil
}

// sweep removes every connection with expiry <= now; if that was an endpoint's
// last connection its external port returns to the pool.
func (t *Table) sweep(now int64) {
	for key, c := range t.conns {
		if c.expiry <= now {
			t.deleteConn(c)
			delete(t.conns, key)
			t.logPrintf("now=%d EXPIRE %s -> %s external=%d expiry=%d (<= now)",
				now, c.key.internal, c.key.remote, c.port, c.expiry)
		}
	}
}

// deleteConn drops the connection's endpoint reference and releases the
// external port when no live connection of that internal endpoint remains.
func (t *Table) deleteConn(c *conn) {
	ep := t.epExt[c.key.internal]
	ep.refs--
	if ep.refs == 0 {
		heap.Push(&t.free, ep.port)
		delete(t.epExt, c.key.internal)
		delete(t.extEP, ep.port)
		t.logPrintf("RELEASE endpoint=%s external port=%d back to pool", c.key.internal, ep.port)
	}
}

func (t *Table) allow(c *conn) Result {
	res := Result{Allowed: true, ExternalPort: c.port, State: c.state, Expiry: c.expiry}
	t.logPrintf("OUTPUT allowed=true external=%d state=%s expiry=%d", c.port, stateName(c.state), c.expiry)
	return res
}

func (t *Table) reject(format string, err error, args ...any) (Result, error) {
	t.logPrintf("REJECT %s: %s", err, fmt.Sprintf(format, args...))
	return Result{}, err
}

func (t *Table) logInput(dir Direction, p Packet, externalPort uint16) {
	if dir == DirInbound {
		t.logPrintf("INPUT dir=inbound now=%d external=%d remote=%s event=%s",
			p.Now, externalPort, p.Remote, eventName(p.Event))
		return
	}
	t.logPrintf("INPUT dir=outbound now=%d internal=%s remote=%s event=%s",
		p.Now, p.Internal, p.Remote, eventName(p.Event))
}

func (t *Table) logPrintf(format string, args ...any) {
	t.log.Printf("[conntrack] "+format, args...)
}

func stateName(s ConnState) string {
	switch s {
	case StateHalfOpen:
		return "half-open"
	case StateEstablished:
		return "established"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

func eventName(e EventType) string {
	switch e {
	case EventSYN:
		return "SYN"
	case EventDATA:
		return "DATA"
	case EventFIN:
		return "FIN"
	case EventRST:
		return "RST"
	default:
		return "unknown"
	}
}

func dirName(d Direction) string {
	if d == DirInbound {
		return "inbound"
	}
	return "outbound"
}

func (e Endpoint) String() string {
	return fmt.Sprintf("%s:%d", e.Addr, e.Port)
}

func endpointLess(a, b Endpoint) bool {
	if ab, bb, ok := addrBytes(a.Addr, b.Addr); ok {
		for i := range ab {
			if ab[i] != bb[i] {
				return ab[i] < bb[i]
			}
		}
	} else if s1, s2 := a.Addr.String(), b.Addr.String(); s1 != s2 {
		return s1 < s2
	}
	return a.Port < b.Port
}

func addrBytes(a, b netip.Addr) ([]byte, []byte, bool) {
	if a.Is4() != b.Is4() {
		return nil, nil, false
	}
	if a.Is4() {
		ab, _ := a.MarshalBinary()
		bb, _ := b.MarshalBinary()
		return ab[len(ab)-4:], bb[len(bb)-4:], true
	}
	ab, _ := a.MarshalBinary()
	bb, _ := b.MarshalBinary()
	return ab, bb, true
}

// portHeap yields the smallest free external port.
type portHeap []uint16

func (h portHeap) Len() int           { return len(h) }
func (h portHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h portHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *portHeap) Push(x any) { *h = append(*h, x.(uint16)) }
func (h *portHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
