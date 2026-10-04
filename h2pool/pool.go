// Package h2pool implements a deterministic HTTP/2 client connection pool
// with stream allocation and graceful shutdown (draining) management.
//
// All methods are safe for concurrent use; the result is equivalent to some
// serial execution order. Replaying the same call sequence yields identical
// connection picks, retry lists and state transitions.
package h2pool

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// State is the lifecycle state of a connection.
type State int

const (
	// Active connections accept new streams.
	Active State = iota
	// Draining connections serve existing streams only and become Closed
	// once their last stream ends.
	Draining
	// Closed connections are terminal.
	Closed
)

func (s State) String() string {
	switch s {
	case Active:
		return "Active"
	case Draining:
		return "Draining"
	case Closed:
		return "Closed"
	}
	return "Unknown"
}

// StreamEndKind describes how a stream ended.
type StreamEndKind int

const (
	// Done: the stream completed normally; clears the refusal counter.
	Done StreamEndKind = iota
	// Refused: the stream was refused (e.g. REFUSED_STREAM); retryable and
	// increments the refusal counter.
	Refused
	// Reset: the stream was reset; not retryable, refusal counter untouched.
	Reset
)

// Distinguishable failure reasons. Use errors.Is to match.
var (
	ErrUnknownConn    = errors.New("h2pool: unknown connection id")
	ErrUnknownStream  = errors.New("h2pool: unknown stream")
	ErrInvalidLastID  = errors.New("h2pool: last-stream-id out of range")
	ErrInvalidMaxConc = errors.New("h2pool: max concurrency out of range")
	ErrInvalidKind    = errors.New("h2pool: unknown stream end kind")
	ErrDuplicateReq   = errors.New("h2pool: duplicate in-flight request")
	ErrDuplicateConn  = errors.New("h2pool: duplicate connection id")
	ErrInvalidParam   = errors.New("h2pool: constructor parameter out of range")
	ErrClockBackwards = errors.New("h2pool: clock moved backwards")
	ErrConnClosed     = errors.New("h2pool: connection is closed")
	ErrGoAwayUp       = errors.New("h2pool: GOAWAY last-stream-id moved up")
	ErrGoAwayBeyond   = errors.New("h2pool: GOAWAY last-stream-id beyond last assigned stream")
	ErrNoCapacity     = errors.New("h2pool: no connection capacity")
)

const (
	maxMaxConc     = 1000
	maxIdleTimeout = int64(1_000_000_000)
	maxStreamID    = int64(1)<<31 - 1
	maxRefuseLimit = 100
)

// conn is the per-connection bookkeeping.
type conn struct {
	id         string
	state      State
	maxConc    int
	nextID     int64
	streams    map[int64]string // stream id -> request id
	goAwayLast int64
	idleSince  int64
	refuse     int
}

// ConnSnapshot is a read-only view of a connection.
type ConnSnapshot struct {
	ID         string
	State      State
	MaxConc    int
	NextID     int64
	Streams    map[int64]string
	GoAwayLast int64
	IdleSince  int64
	Refuse     int
}

// Pool manages a set of HTTP/2 client connections.
type Pool struct {
	mu          sync.Mutex
	maxConc0    int
	idleTimeout int64
	maxID       int64
	refuseLimit int
	conns       map[string]*conn
	inflight    map[string]struct{} // request ids currently in flight
	lastNow     int64
	clockSet    bool
}

// NewPool validates the constructor parameters and returns an empty pool.
//
//	m0:          initial per-connection concurrency cap, 1..1000
//	idleTimeout: idle timeout in milliseconds, 1..1e9
//	maxID:       highest allocatable stream id, odd, 1..2^31-1
//	k:           consecutive-refusal threshold, 1..100
func NewPool(m0 int, idleTimeout int64, maxID int64, k int) (*Pool, error) {
	if m0 < 1 || m0 > maxMaxConc {
		return nil, fmt.Errorf("%w: m0=%d not in [1,%d]", ErrInvalidParam, m0, maxMaxConc)
	}
	if idleTimeout < 1 || idleTimeout > maxIdleTimeout {
		return nil, fmt.Errorf("%w: idleTimeout=%d not in [1,%d]", ErrInvalidParam, idleTimeout, maxIdleTimeout)
	}
	if maxID < 1 || maxID > maxStreamID || maxID%2 == 0 {
		return nil, fmt.Errorf("%w: maxID=%d must be odd and in [1,2^31-1]", ErrInvalidParam, maxID)
	}
	if k < 1 || k > maxRefuseLimit {
		return nil, fmt.Errorf("%w: k=%d not in [1,%d]", ErrInvalidParam, k, maxRefuseLimit)
	}
	return &Pool{
		maxConc0:    m0,
		idleTimeout: idleTimeout,
		maxID:       maxID,
		refuseLimit: k,
		conns:       make(map[string]*conn),
		inflight:    make(map[string]struct{}),
	}, nil
}

// checkClock reports whether now is earlier than the last accepted call.
// It does not mutate anything; accepted calls must commit via acceptClock
// only after every rejection check has passed, so that rejected calls never
// move the clock.
func (p *Pool) checkClock(now int64) error {
	if p.clockSet && now < p.lastNow {
		return fmt.Errorf("%w: now=%d < last=%d", ErrClockBackwards, now, p.lastNow)
	}
	return nil
}

// acceptClock commits now as the last accepted timestamp.
func (p *Pool) acceptClock(now int64) {
	p.lastNow = now
	p.clockSet = true
}

// AddConn adds a new Active connection. Ids are unique for the lifetime of
// the pool, including ids of already Closed connections.
func (p *Pool) AddConn(now int64, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.conns[id]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateConn, id)
	}
	if err := p.checkClock(now); err != nil {
		return err
	}
	p.acceptClock(now)
	p.conns[id] = &conn{
		id:         id,
		state:      Active,
		maxConc:    p.maxConc0,
		nextID:     1,
		streams:    make(map[int64]string),
		goAwayLast: p.maxID,
		idleSince:  now,
	}
	return nil
}

// Open allocates a stream for req on the Active connection with the fewest
// active streams (ties broken by smallest id) whose active stream count is
// strictly below its concurrency cap. It returns the connection id and the
// allocated stream id.
func (p *Pool) Open(now int64, req string) (string, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.inflight[req]; dup {
		return "", 0, fmt.Errorf("%w: %q", ErrDuplicateReq, req)
	}
	if err := p.checkClock(now); err != nil {
		return "", 0, err
	}
	var best *conn
	for _, id := range p.sortedIDs() {
		c := p.conns[id]
		if c.state != Active || len(c.streams) >= c.maxConc {
			continue
		}
		if best == nil || len(c.streams) < len(best.streams) {
			best = c
		}
	}
	if best == nil {
		return "", 0, ErrNoCapacity
	}
	p.acceptClock(now)
	stream := best.nextID
	best.nextID += 2
	if best.nextID > p.maxID {
		best.state = Draining
	}
	best.streams[stream] = req
	p.inflight[req] = struct{}{}
	return best.id, stream, nil
}

// SetMaxConcurrent changes the concurrency cap of a connection. Existing
// streams are never cancelled; m may be below the current active count.
func (p *Pool) SetMaxConcurrent(now int64, id string, m int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownConn, id)
	}
	if m < 1 || m > maxMaxConc {
		return fmt.Errorf("%w: m=%d not in [1,%d]", ErrInvalidMaxConc, m, maxMaxConc)
	}
	if err := p.checkClock(now); err != nil {
		return err
	}
	if c.state == Closed {
		return fmt.Errorf("%w: %q", ErrConnClosed, id)
	}
	p.acceptClock(now)
	c.maxConc = m
	return nil
}

// CloseStream removes a stream from a connection and reports whether the
// request may be safely retried (true only for Refused).
func (p *Pool) CloseStream(now int64, id string, stream int64, kind StreamEndKind) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownConn, id)
	}
	req, ok := c.streams[stream]
	if !ok {
		return false, fmt.Errorf("%w: conn=%q stream=%d", ErrUnknownStream, id, stream)
	}
	if kind != Done && kind != Refused && kind != Reset {
		return false, fmt.Errorf("%w: %d", ErrInvalidKind, kind)
	}
	if err := p.checkClock(now); err != nil {
		return false, err
	}
	p.acceptClock(now)
	delete(c.streams, stream)
	delete(p.inflight, req)
	retryable := false
	switch kind {
	case Done:
		c.refuse = 0
	case Refused:
		c.refuse++
		retryable = true
		if c.refuse >= p.refuseLimit && c.state == Active {
			c.state = Draining
		}
	case Reset:
	}
	if len(c.streams) == 0 {
		switch c.state {
		case Draining:
			c.state = Closed
		case Active:
			c.idleSince = now
		}
	}
	return retryable, nil
}

// GoAway processes a GOAWAY frame with the given last-stream-id. It returns
// the request ids of streams above lastID (unprocessed, safe to retry),
// ordered by ascending stream id.
func (p *Pool) GoAway(now int64, id string, lastID int64) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownConn, id)
	}
	if lastID != 0 && (lastID < 0 || lastID > p.maxID || lastID%2 == 0) {
		return nil, fmt.Errorf("%w: lastID=%d", ErrInvalidLastID, lastID)
	}
	if err := p.checkClock(now); err != nil {
		return nil, err
	}
	if c.state == Closed {
		return nil, fmt.Errorf("%w: %q", ErrConnClosed, id)
	}
	if lastID > c.goAwayLast {
		return nil, fmt.Errorf("%w: lastID=%d > goAwayLast=%d", ErrGoAwayUp, lastID, c.goAwayLast)
	}
	lastAssigned := c.nextID - 2
	if lastAssigned < 0 {
		lastAssigned = 0
	}
	if lastID > lastAssigned && lastID != p.maxID {
		return nil, fmt.Errorf("%w: lastID=%d > lastAssigned=%d", ErrGoAwayBeyond, lastID, lastAssigned)
	}
	p.acceptClock(now)
	c.goAwayLast = lastID
	if c.state == Active {
		c.state = Draining
	}
	doomed := make([]int64, 0, len(c.streams))
	for s := range c.streams {
		if s > lastID {
			doomed = append(doomed, s)
		}
	}
	sort.Slice(doomed, func(i, j int) bool { return doomed[i] < doomed[j] })
	retry := make([]string, 0, len(doomed))
	for _, s := range doomed {
		retry = append(retry, c.streams[s])
		delete(p.inflight, c.streams[s])
		delete(c.streams, s)
	}
	if len(c.streams) == 0 {
		c.state = Closed
	}
	return retry, nil
}

// Tick closes Active connections that have been idle (no active streams) for
// at least idleTimeout milliseconds. It returns the closed ids in ascending
// order.
func (p *Pool) Tick(now int64) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkClock(now); err != nil {
		return nil, err
	}
	p.acceptClock(now)
	var closed []string
	for _, id := range p.sortedIDs() {
		c := p.conns[id]
		if c.state == Active && len(c.streams) == 0 && now-c.idleSince >= p.idleTimeout {
			c.state = Closed
			closed = append(closed, id)
		}
	}
	return closed, nil
}

// Snapshot returns a copy of a connection's bookkeeping.
func (p *Pool) Snapshot(id string) (ConnSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return ConnSnapshot{}, fmt.Errorf("%w: %q", ErrUnknownConn, id)
	}
	streams := make(map[int64]string, len(c.streams))
	for s, r := range c.streams {
		streams[s] = r
	}
	return ConnSnapshot{
		ID:         c.id,
		State:      c.state,
		MaxConc:    c.maxConc,
		NextID:     c.nextID,
		Streams:    streams,
		GoAwayLast: c.goAwayLast,
		IdleSince:  c.idleSince,
		Refuse:     c.refuse,
	}, nil
}

// sortedIDs returns all connection ids in ascending order.
func (p *Pool) sortedIDs() []string {
	ids := make([]string, 0, len(p.conns))
	for id := range p.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
