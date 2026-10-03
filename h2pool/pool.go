// Package h2pool implements a deterministic HTTP/2 client connection pool
// with stream allocation across connections and graceful shutdown (drain)
// management.
//
// All methods are safe for concurrent use; the result is equivalent to some
// serial execution order. Replaying the same call sequence yields identical
// connection choices, retry lists and state transitions.
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
	// Draining connections serve existing streams but accept no new ones.
	Draining
	// Closed connections are fully drained and shut down.
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
	return fmt.Sprintf("State(%d)", int(s))
}

// CloseKind describes how an active stream ended.
type CloseKind int

const (
	// Done: the stream completed normally; resets the refusal counter.
	Done CloseKind = iota
	// Refused: the stream was refused (e.g. REFUSED_STREAM); retryable and
	// increments the refusal counter.
	Refused
	// Reset: the stream was reset; not retryable, refusal counter unchanged.
	Reset
)

func (k CloseKind) String() string {
	switch k {
	case Done:
		return "Done"
	case Refused:
		return "Refused"
	case Reset:
		return "Reset"
	}
	return fmt.Sprintf("CloseKind(%d)", int(k))
}

// ErrCode identifies the single, first-detected reason a call was rejected.
type ErrCode int

const (
	// ErrUnknownConn: the connection id does not exist (including Closed).
	ErrUnknownConn ErrCode = iota + 1
	// ErrUnknownStream: the stream is not in the connection's active table.
	ErrUnknownStream
	// ErrBadLastID: lastID is neither 0 nor an odd number in [1, maxID].
	ErrBadLastID
	// ErrBadMaxConc: m is outside [1, 1000].
	ErrBadMaxConc
	// ErrBadNow: now is outside [0, 1e12].
	ErrBadNow
	// ErrDupReq: the request identifier is already in flight.
	ErrDupReq
	// ErrBadConfig: a constructor argument is out of range.
	ErrBadConfig
	// ErrDupConn: the connection id is already in use (including Closed).
	ErrDupConn
	// ErrBadKind: the CloseKind is not Done, Refused or Reset.
	ErrBadKind
	// ErrClockBackwards: now is earlier than the last accepted call's now.
	ErrClockBackwards
	// ErrConnClosed: GoAway/SetMaxConcurrent on a Closed connection.
	ErrConnClosed
	// ErrGoAwayUp: lastID is greater than the current goAwayLast.
	ErrGoAwayUp
	// ErrGoAwayBeyond: lastID exceeds the last allocated stream and is not maxID.
	ErrGoAwayBeyond
	// ErrNoCapacity: no Active connection has room for another stream.
	ErrNoCapacity
)

func (c ErrCode) String() string {
	switch c {
	case ErrUnknownConn:
		return "ErrUnknownConn"
	case ErrUnknownStream:
		return "ErrUnknownStream"
	case ErrBadLastID:
		return "ErrBadLastID"
	case ErrBadMaxConc:
		return "ErrBadMaxConc"
	case ErrBadNow:
		return "ErrBadNow"
	case ErrDupReq:
		return "ErrDupReq"
	case ErrBadConfig:
		return "ErrBadConfig"
	case ErrDupConn:
		return "ErrDupConn"
	case ErrBadKind:
		return "ErrBadKind"
	case ErrClockBackwards:
		return "ErrClockBackwards"
	case ErrConnClosed:
		return "ErrConnClosed"
	case ErrGoAwayUp:
		return "ErrGoAwayUp"
	case ErrGoAwayBeyond:
		return "ErrGoAwayBeyond"
	case ErrNoCapacity:
		return "ErrNoCapacity"
	}
	return fmt.Sprintf("ErrCode(%d)", int(c))
}

// Error is the single error type returned by this package.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// CodeOf extracts the ErrCode of an error returned by this package, or 0.
func CodeOf(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func newErr(code ErrCode, format string, args ...interface{}) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

const (
	maxNow             = int64(1_000_000_000_000)
	maxIdleTimeout     = int64(1_000_000_000)
	maxStreamID        = int64(1<<31 - 1)
	maxConcLimit       = 1000
	maxRefuseThreshold = 100
)

// ConnSnapshot is a read-only view of one connection's state.
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

type conn struct {
	id         string
	state      State
	maxConc    int
	nextID     int64
	streams    map[int64]string
	goAwayLast int64
	idleSince  int64
	refuse     int
}

// Pool is the connection pool / graceful shutdown manager.
type Pool struct {
	mu          sync.Mutex
	m0          int
	idleTimeout int64
	maxID       int64
	k           int
	conns       map[string]*conn
	inFlight    map[string]string // req -> conn id
	lastNow     int64
	hasNow      bool
}

// NewPool validates the configuration and returns an empty pool.
func NewPool(m0 int, idleTimeout, maxID int64, k int) (*Pool, error) {
	if m0 < 1 || m0 > maxConcLimit {
		return nil, newErr(ErrBadConfig, "m0 %d out of range [1,%d]", m0, maxConcLimit)
	}
	if idleTimeout < 1 || idleTimeout > maxIdleTimeout {
		return nil, newErr(ErrBadConfig, "idleTimeout %d out of range [1,%d]", idleTimeout, maxIdleTimeout)
	}
	if maxID < 1 || maxID > maxStreamID || maxID%2 == 0 {
		return nil, newErr(ErrBadConfig, "maxID %d must be odd and in [1,%d]", maxID, maxStreamID)
	}
	if k < 1 || k > maxRefuseThreshold {
		return nil, newErr(ErrBadConfig, "K %d out of range [1,%d]", k, maxRefuseThreshold)
	}
	return &Pool{
		m0:          m0,
		idleTimeout: idleTimeout,
		maxID:       maxID,
		k:           k,
		conns:       make(map[string]*conn),
		inFlight:    make(map[string]string),
	}, nil
}

// checkNow validates the timestamp range (parameter phase) and the
// monotonic-clock rule (clock phase). It does not advance the clock; the
// caller must call acceptNow only after every other validation passed, so
// that rejected calls never move the clock.
func (p *Pool) checkNow(now int64) error {
	if now < 0 || now > maxNow {
		return newErr(ErrBadNow, "now %d out of range [0,%d]", now, maxNow)
	}
	if p.hasNow && now < p.lastNow {
		return newErr(ErrClockBackwards, "now %d < last accepted now %d", now, p.lastNow)
	}
	return nil
}

func (p *Pool) acceptNow(now int64) {
	p.lastNow = now
	p.hasNow = true
}

// AddConn joins a new Active connection. The id must never have been used,
// including by connections that are already Closed.
func (p *Pool) AddConn(now int64, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.conns[id]; dup {
		return newErr(ErrDupConn, "conn id %q already used", id)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	p.acceptNow(now)
	p.conns[id] = &conn{
		id:         id,
		state:      Active,
		maxConc:    p.m0,
		nextID:     1,
		streams:    make(map[int64]string),
		goAwayLast: p.maxID,
		idleSince:  now,
	}
	return nil
}

// Open allocates a stream for req on the Active connection with the fewest
// active streams among those strictly below their concurrency limit; ties
// break to the smallest id. It returns the chosen conn id and stream number.
func (p *Pool) Open(now int64, req string) (string, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.inFlight[req]; dup {
		return "", 0, newErr(ErrDupReq, "req %q already in flight", req)
	}
	if err := p.checkNow(now); err != nil {
		return "", 0, err
	}
	var best *conn
	for _, c := range p.conns {
		if c.state != Active || len(c.streams) >= c.maxConc {
			continue
		}
		if best == nil || len(c.streams) < len(best.streams) ||
			(len(c.streams) == len(best.streams) && c.id < best.id) {
			best = c
		}
	}
	if best == nil {
		return "", 0, newErr(ErrNoCapacity, "no Active connection below its concurrency limit")
	}
	p.acceptNow(now)
	stream := best.nextID
	best.streams[stream] = req
	p.inFlight[req] = best.id
	best.nextID += 2
	if best.nextID > p.maxID {
		// maxID was the last allocatable stream number; the connection
		// can never open another stream, so start draining.
		best.state = Draining
	}
	return best.id, stream, nil
}

// SetMaxConcurrent changes only the connection's concurrency limit. Active
// streams are never cancelled, so m below the current active count succeeds.
func (p *Pool) SetMaxConcurrent(now int64, id string, m int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return newErr(ErrUnknownConn, "unknown conn id %q", id)
	}
	if m < 1 || m > maxConcLimit {
		return newErr(ErrBadMaxConc, "m %d out of range [1,%d]", m, maxConcLimit)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	if c.state == Closed {
		return newErr(ErrConnClosed, "conn %q is Closed", id)
	}
	p.acceptNow(now)
	c.maxConc = m
	return nil
}

// CloseStream removes stream from the connection's active table and reports
// whether the request is safe to retry (only for Refused).
func (p *Pool) CloseStream(now int64, id string, stream int64, kind CloseKind) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return false, newErr(ErrUnknownConn, "unknown conn id %q", id)
	}
	req, ok := c.streams[stream]
	if !ok {
		return false, newErr(ErrUnknownStream, "conn %q has no active stream %d", id, stream)
	}
	if kind != Done && kind != Refused && kind != Reset {
		return false, newErr(ErrBadKind, "unknown close kind %d", int(kind))
	}
	if err := p.checkNow(now); err != nil {
		return false, err
	}
	p.acceptNow(now)
	delete(c.streams, stream)
	delete(p.inFlight, req)
	retryable := false
	switch kind {
	case Done:
		c.refuse = 0
	case Refused:
		c.refuse++
		retryable = true
		if c.refuse >= p.k && c.state == Active {
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

// GoAway processes a GOAWAY frame with the given last-stream-id. Streams
// numbered above lastID are unprocessed: they are removed and their request
// identifiers returned in ascending stream order, safe to retry.
func (p *Pool) GoAway(now int64, id string, lastID int64) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return nil, newErr(ErrUnknownConn, "unknown conn id %q", id)
	}
	if lastID != 0 && (lastID < 0 || lastID > p.maxID || lastID%2 == 0) {
		return nil, newErr(ErrBadLastID, "lastID %d must be 0 or odd in [1,%d]", lastID, p.maxID)
	}
	if err := p.checkNow(now); err != nil {
		return nil, err
	}
	if c.state == Closed {
		return nil, newErr(ErrConnClosed, "conn %q is Closed", id)
	}
	if lastID > c.goAwayLast {
		return nil, newErr(ErrGoAwayUp, "lastID %d > goAwayLast %d", lastID, c.goAwayLast)
	}
	lastAlloc := int64(0)
	if c.nextID > 1 {
		lastAlloc = c.nextID - 2
	}
	if lastID > lastAlloc && lastID != p.maxID {
		return nil, newErr(ErrGoAwayBeyond, "lastID %d beyond last allocated stream %d", lastID, lastAlloc)
	}
	p.acceptNow(now)
	c.goAwayLast = lastID
	if c.state == Active {
		c.state = Draining
	}
	var doomed []int64
	for s := range c.streams {
		if s > lastID {
			doomed = append(doomed, s)
		}
	}
	sort.Slice(doomed, func(i, j int) bool { return doomed[i] < doomed[j] })
	unprocessed := make([]string, 0, len(doomed))
	for _, s := range doomed {
		unprocessed = append(unprocessed, c.streams[s])
		delete(p.inFlight, c.streams[s])
		delete(c.streams, s)
	}
	if len(c.streams) == 0 {
		c.state = Closed
	}
	return unprocessed, nil
}

// Tick closes Active connections that have been idle (no active streams) for
// at least idleTimeout milliseconds, and returns their ids in ascending order.
func (p *Pool) Tick(now int64) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNow(now); err != nil {
		return nil, err
	}
	p.acceptNow(now)
	closed := []string{}
	for _, c := range p.conns {
		if c.state == Active && len(c.streams) == 0 && now-c.idleSince >= p.idleTimeout {
			c.state = Closed
			closed = append(closed, c.id)
		}
	}
	sort.Strings(closed)
	return closed, nil
}

// Snapshot returns a consistent read-only view of one connection.
func (p *Pool) Snapshot(id string) (ConnSnapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.conns[id]
	if !ok {
		return ConnSnapshot{}, false
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
	}, true
}
