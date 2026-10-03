// Package syncookie implements a SYN-cookie handshake validator with a
// bounded half-open queue. While the half-open queue has room, inbound SYNs
// are registered as stateful half-open entries; once it is full, the
// validator switches to stateless SYN cookies. Incoming ACKs are verified
// either against a half-open entry or against the cookie, and established
// connections are placed into a bounded pending (accept) queue.
package syncookie

import (
	"errors"
	"fmt"
	"sync"
)

// mssTable holds the MSS values encoded into cookies at bits 24..26.
var mssTable = [4]uint16{536, 1220, 1460, 8960}

const (
	maxNow       = int64(1_000_000_000_000) // 1e12 ms
	maxQueueCap  = 1024
	maxTimeout   = 1_000_000 // ms
	tickMillis   = 64000
	cookieMaxAge = 1 // in ticks; ages 0 and 1 are accepted
)

// Distinguishable rejection/failure reasons. Validation failures
// (ErrInvalidNow, ErrInvalidMSS, ErrClockBackwards) are reported before any
// operational reason and leave all state untouched.
var (
	ErrInvalidNow     = errors.New("syncookie: now out of range [0, 1e12]")
	ErrInvalidMSS     = errors.New("syncookie: mss out of range [0, 65535]")
	ErrClockBackwards = errors.New("syncookie: now earlier than last accepted operation")
	ErrAcceptFull     = errors.New("syncookie: accept (pending) queue full")
	ErrBadAck         = errors.New("syncookie: ack/seq does not match half-open entry")
	ErrCookie         = errors.New("syncookie: cookie invalid")
	ErrCookieExpired  = errors.New("syncookie: cookie expired")
	ErrEmpty          = errors.New("syncookie: accept (pending) queue empty")
)

// ErrInvalidParam wraps constructor parameter violations.
var ErrInvalidParam = errors.New("syncookie: invalid constructor parameter")

// Key identifies a connection: (caddr, cport, saddr, sport).
type Key struct {
	CAddr uint32
	CPort uint16
	SAddr uint32
	SPort uint16
}

// HashFunc is injected to compute the cookie's 24-bit hash. t is the tick
// (now/64000) used at cookie issuance.
type HashFunc func(caddr, saddr uint32, cport, sport uint16, cisn, t uint32) uint32

// SynResult is the response to an accepted OnSyn.
type SynResult struct {
	ISN    uint32
	Cookie bool
}

// Stats is a point-in-time snapshot of validator counters. HalfOpen reflects
// the half-open queue as cleaned by the most recent accepted operation.
type Stats struct {
	HalfOpen   int
	Pending    int
	CookieSent uint64
	CookieOK   uint64
	Retrans    uint64
}

type halfEntry struct {
	key     Key
	sisn    uint32
	cisn    uint32
	mss     uint16
	created int64
}

type pendingEntry struct {
	key Key
	mss uint16
}

// Validator is safe for concurrent use; results are equivalent to some
// serial order of the operations.
type Validator struct {
	mu      sync.Mutex
	b       int
	t       int64 // half-open timeout in ms
	a       int
	hash    HashFunc
	nextISN func() uint32

	half    map[Key]*halfEntry
	pending []pendingEntry

	cookieSent uint64
	cookieOK   uint64
	retrans    uint64

	lastNow int64
	hasNow  bool
}

// New constructs a Validator. b is the half-open capacity (1..1024),
// timeout the half-open timeout in ms (1..1e6), a the pending queue
// capacity (1..1024). hash and nextISN must be non-nil.
func New(b int, timeout int64, a int, hash HashFunc, nextISN func() uint32) (*Validator, error) {
	if b < 1 || b > maxQueueCap {
		return nil, fmt.Errorf("%w: half-open capacity %d out of range [1, %d]", ErrInvalidParam, b, maxQueueCap)
	}
	if timeout < 1 || timeout > maxTimeout {
		return nil, fmt.Errorf("%w: timeout %d out of range [1, %d]", ErrInvalidParam, timeout, maxTimeout)
	}
	if a < 1 || a > maxQueueCap {
		return nil, fmt.Errorf("%w: accept queue capacity %d out of range [1, %d]", ErrInvalidParam, a, maxQueueCap)
	}
	if hash == nil {
		return nil, fmt.Errorf("%w: hash function is nil", ErrInvalidParam)
	}
	if nextISN == nil {
		return nil, fmt.Errorf("%w: nextISN function is nil", ErrInvalidParam)
	}
	return &Validator{
		b:       b,
		t:       timeout,
		a:       a,
		hash:    hash,
		nextISN: nextISN,
		half:    make(map[Key]*halfEntry),
	}, nil
}

// tickOf converts a millisecond timestamp to its tick number.
func tickOf(now int64) uint32 {
	return uint32(now / tickMillis)
}

// mssIndex returns the index of the largest table entry not exceeding mss,
// or 0 when mss is smaller than the smallest entry.
func mssIndex(mss uint16) uint32 {
	mi := 0
	for i, v := range mssTable {
		if v <= mss {
			mi = i
		}
	}
	return uint32(mi)
}

// validateNow checks the timestamp range and clock monotonicity. It reports
// ErrInvalidNow before ErrClockBackwards.
func (v *Validator) validateNow(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidNow
	}
	if v.hasNow && now < v.lastNow {
		return ErrClockBackwards
	}
	return nil
}

// accept marks the operation as accepted: advances the clock and removes
// expired half-open entries (created+T <= now; exactly equal is expired).
func (v *Validator) accept(now int64) {
	v.lastNow = now
	v.hasNow = true
	for k, e := range v.half {
		if e.created+v.t <= now {
			delete(v.half, k)
		}
	}
}

// OnSyn processes an inbound SYN. On success it returns the ISN to put in
// the SYN-ACK and whether that ISN is a stateless cookie.
func (v *Validator) OnSyn(now int64, key Key, cisn uint32, mss int) (SynResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.validateNow(now); err != nil {
		return SynResult{}, err
	}
	if mss < 0 || mss > 65535 {
		return SynResult{}, ErrInvalidMSS
	}
	v.accept(now)

	// (1) retransmission of a known half-open SYN.
	if e, ok := v.half[key]; ok {
		v.retrans++
		return SynResult{ISN: e.sisn, Cookie: false}, nil
	}
	// (2) accept queue full: drop.
	if len(v.pending) >= v.a {
		return SynResult{}, ErrAcceptFull
	}
	// (3) half-open queue has room: register state.
	if len(v.half) < v.b {
		sisn := v.nextISN()
		v.half[key] = &halfEntry{key: key, sisn: sisn, cisn: cisn, mss: uint16(mss), created: now}
		return SynResult{ISN: sisn, Cookie: false}, nil
	}
	// (4) half-open full: stateless cookie, no state stored.
	t := tickOf(now)
	mi := mssIndex(uint16(mss))
	h := v.hash(key.CAddr, key.SAddr, key.CPort, key.SPort, cisn, t) & 0xFFFFFF
	isn := (t % 32) << 27
	isn |= mi << 24
	isn |= h
	v.cookieSent++
	return SynResult{ISN: isn, Cookie: true}, nil
}

// OnAck processes the ACK completing a handshake. A nil error means the
// connection is established and has been appended to the pending queue.
func (v *Validator) OnAck(now int64, key Key, seq, ack uint32) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.validateNow(now); err != nil {
		return err
	}
	v.accept(now)

	if e, ok := v.half[key]; ok {
		if ack != e.sisn+1 || seq != e.cisn+1 {
			return ErrBadAck
		}
		if len(v.pending) >= v.a {
			return ErrAcceptFull
		}
		delete(v.half, key)
		v.pending = append(v.pending, pendingEntry{key: key, mss: e.mss})
		return nil
	}

	// Stateless cookie path.
	c := ack - 1
	t5 := c >> 27
	mi := (c >> 24) & 7
	h := c & 0xFFFFFF
	if mi >= 4 {
		return ErrCookie
	}
	t := tickOf(now)
	age := (t%32 - t5 + 32) % 32
	if age > cookieMaxAge {
		return ErrCookieExpired
	}
	t0 := t - age // uint32 wraparound is intentional
	if v.hash(key.CAddr, key.SAddr, key.CPort, key.SPort, seq-1, t0)&0xFFFFFF != h {
		return ErrCookie
	}
	if len(v.pending) >= v.a {
		return ErrAcceptFull
	}
	v.pending = append(v.pending, pendingEntry{key: key, mss: mssTable[mi]})
	v.cookieOK++
	return nil
}

// Accept removes and returns the oldest established connection.
func (v *Validator) Accept() (Key, uint16, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(v.pending) == 0 {
		return Key{}, 0, ErrEmpty
	}
	e := v.pending[0]
	v.pending = v.pending[1:]
	return e.key, e.mss, nil
}

// Stats returns a snapshot of the counters.
func (v *Validator) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Stats{
		HalfOpen:   len(v.half),
		Pending:    len(v.pending),
		CookieSent: v.cookieSent,
		CookieOK:   v.cookieOK,
		Retrans:    v.retrans,
	}
}
