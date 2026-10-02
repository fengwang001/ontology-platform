// Package syncookie implements a SYN handshake validator that registers
// pending connections in a bounded half-open queue and, when that queue is
// full, falls back to stateless SYN cookies.
package syncookie

import "sync"

// Key identifies a TCP-like connection by its address/port four-tuple.
type Key struct {
	CAddr uint32
	CPort uint16
	SAddr uint32
	SPort uint16
}

// HashFunc is the injected hash primitive used to build and verify cookies.
type HashFunc func(caddr, saddr uint32, cport, sport uint16, cisn, t uint32) uint32

// NextISNFunc returns the next server initial sequence number.
type NextISNFunc func() uint32

// SynReply is the result of OnSyn.
type SynReply struct {
	ISN    uint32
	Cookie bool
}

// Stats is the snapshot returned by Stats.
type Stats struct {
	HalfOpen   int // number of live half-open entries after the latest accepted op's cleanup
	Acceptable int // number of connections waiting in the accept queue
	CookieSent int // stateless SYN cookies sent
	CookieOK   int // cookie ACKs that validated
	Retrans    int // retransmitted SYNs for existing half-open keys
}

// MSS table is fixed: index 0..3.
var mssTable = [4]uint32{536, 1220, 1460, 8960}

const (
	maxNow    int64 = 1_000_000_000_000
	msPerTick int64 = 64_000
)

type halfOpen struct {
	key     Key
	sisn    uint32
	cisn    uint32
	mss     uint32
	created int64
}

// Validator is the concurrent-safe handshake validator.
type Validator struct {
	mu      sync.Mutex
	b       int
	a       int
	t       int64
	hash    HashFunc
	nextISN NextISNFunc

	half    map[Key]*halfOpen
	acceptQ []accepted

	lastNow    int64
	haveTime   bool
	cookieSent int
	cookieOK   int
	retrans    int
}

type accepted struct {
	key Key
	mss uint32
}

// New constructs a Validator. Stub.
func New(B int, T int64, A int, hash HashFunc, nextISN NextISNFunc) (*Validator, error) {
	if B < 1 || B > 1024 || A < 1 || A > 1024 || T < 1 || T > 1_000_000 || hash == nil || nextISN == nil {
		return nil, ErrInvalidParam
	}
	return &Validator{
		b:       B,
		a:       A,
		t:       T,
		hash:    hash,
		nextISN: nextISN,
		half:    make(map[Key]*halfOpen),
	}, nil
}

// checkTime rejects illegal timestamps and clock rollback. It is reported
// before every other reason. On success the operation is "accepted" and may
// touch the queues; on failure nothing is changed and no cleanup happens.
func (v *Validator) checkTime(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if v.haveTime && now < v.lastNow {
		return ErrClockBack
	}
	return nil
}

// prune removes half-open entries with created+T <= now (equality expires).
func (v *Validator) prune(now int64) {
	for key, h := range v.half {
		if h.created+v.t <= now {
			delete(v.half, key)
		}
	}
}

func mssIndex(mss uint32) uint32 {
	mi := uint32(0)
	for i := range mssTable {
		if mssTable[i] <= mss {
			mi = uint32(i)
		}
	}
	return mi
}

// OnSyn handles a SYN segment.
func (v *Validator) OnSyn(now int64, key Key, cisn uint32, mss uint32) (SynReply, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.checkTime(now); err != nil {
		return SynReply{}, err
	}
	if mss > 65535 {
		return SynReply{}, ErrInvalidMSS
	}

	v.prune(now)
	v.lastNow = now
	v.haveTime = true

	// (一) retransmission of an existing half-open key.
	if h, ok := v.half[key]; ok {
		v.retrans++
		return SynReply{ISN: h.sisn, Cookie: false}, nil
	}

	// (二) accept queue already full: drop.
	if len(v.acceptQ) >= v.a {
		return SynReply{}, ErrAcceptFull
	}

	// (三) free half-open slot: register state.
	if len(v.half) < v.b {
		h := &halfOpen{
			key:     key,
			sisn:    v.nextISN(),
			cisn:    cisn,
			mss:     mss,
			created: now,
		}
		v.half[key] = h
		return SynReply{ISN: h.sisn, Cookie: false}, nil
	}

	// (四) half-open queue full: emit a stateless cookie, store nothing.
	tick := uint32(now / msPerTick)
	mi := mssIndex(mss)
	hv := v.hash(key.CAddr, key.SAddr, key.CPort, key.SPort, cisn, tick) & 0xFFFFFF
	isn := (tick%32)<<27 | mi<<24 | hv
	v.cookieSent++
	return SynReply{ISN: isn, Cookie: true}, nil
}

// OnAck handles the final ACK of the handshake.
func (v *Validator) OnAck(now int64, key Key, seq, ack uint32) (mss uint32, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.checkTime(now); err != nil {
		return 0, err
	}

	v.prune(now)
	v.lastNow = now
	v.haveTime = true

	tick := uint32(now / msPerTick)

	// Half-open path.
	if h, ok := v.half[key]; ok {
		if ack != h.sisn+1 || seq != h.cisn+1 {
			return 0, ErrBadAck // entry retained
		}
		if len(v.acceptQ) >= v.a {
			return 0, ErrAcceptFull // entry retained
		}
		delete(v.half, key)
		v.acceptQ = append(v.acceptQ, accepted{key: key, mss: h.mss})
		return h.mss, nil
	}

	// Cookie path. c = ack - 1.
	c := ack - 1
	t5 := c >> 27
	mi := (c >> 24) & 7
	hv := c & 0xFFFFFF

	if mi >= uint32(len(mssTable)) {
		return 0, ErrCookie
	}
	age := (tick - t5) & 31
	if age > 1 {
		return 0, ErrCookieExpired
	}
	t0 := tick - age // uint32 wrap-around
	want := v.hash(key.CAddr, key.SAddr, key.CPort, key.SPort, seq-1, t0) & 0xFFFFFF
	if want != hv {
		return 0, ErrCookie
	}
	if len(v.acceptQ) >= v.a {
		return 0, ErrAcceptFull
	}
	negotiated := mssTable[mi]
	v.acceptQ = append(v.acceptQ, accepted{key: key, mss: negotiated})
	v.cookieOK++
	return negotiated, nil
}

// Accept removes and returns the oldest established connection.
func (v *Validator) Accept() (key Key, mss uint32, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.acceptQ) == 0 {
		return Key{}, 0, ErrEmpty
	}
	first := v.acceptQ[0]
	v.acceptQ = v.acceptQ[1:]
	return first.key, first.mss, nil
}

// Stats returns the current counters.
func (v *Validator) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return Stats{
		HalfOpen:   len(v.half),
		Acceptable: len(v.acceptQ),
		CookieSent: v.cookieSent,
		CookieOK:   v.cookieOK,
		Retrans:    v.retrans,
	}
}
