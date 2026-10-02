// Package cidpool implements a QUIC-style connection ID pool manager.
//
// The pool accepts connection IDs advertised by the peer, retires them in
// batches driven by retire_prior_to, enforces an active-count limit and
// duplicate/conflict rules, and assigns each path a distinct active
// connection ID. All operations are safe for concurrent use.
package cidpool

import (
	"errors"
	"sort"
	"sync"
)

// Distinguishable rejection reasons.
var (
	// ErrEncoding is reported for malformed frames: rpt > seq, a CID whose
	// length is outside 1..20, or a reset token whose length is not 16.
	ErrEncoding = errors.New("cidpool: encoding error")
	// ErrViolation is reported for protocol conflicts: a known seq carrying
	// a different (cid, token), or a new seq reusing a cid already known
	// (including retired cids kept as tombstones).
	ErrViolation = errors.New("cidpool: protocol violation")
	// ErrLimit is reported when accepting a frame would make the number of
	// active entries exceed the configured limit L.
	ErrLimit = errors.New("cidpool: active connection id limit exceeded")
	// ErrArg is reported for bad local arguments: an existing pathID for
	// NewPath, an unknown pathID for FreePath/PathSeq, or Retire of a seq
	// that is not a known active entry.
	ErrArg = errors.New("cidpool: invalid argument")
	// ErrNoCID is reported by NewPath when no unoccupied active entry exists.
	ErrNoCID = errors.New("cidpool: no connection id available")
)

const (
	maxSeq    = (1 << 62) - 1
	minCIDLen = 1
	maxCIDLen = 20
	tokenLen  = 16
	smallestL = 2
	largestL  = 16
)

// entry is one connection-ID slot. Retired entries remain forever as
// tombstones so that their cid can never be reused by another seq.
type entry struct {
	seq     uint64
	cid     []byte
	token   [tokenLen]byte
	retired bool
}

// Pool is the connection ID pool. The zero value is not usable; create one
// with NewPool.
type Pool struct {
	mu       sync.Mutex
	limit    uint64
	entries  map[uint64]*entry
	retiredR uint64
	retires  []uint64
	paths    map[uint64]pathSlot
}

type pathSlot struct {
	seq    uint64
	parked bool
}

// NewPool creates a pool with active limit L (2..16), the initial
// connection ID cid0 (1..20 bytes) bound to seq 0 and its 16-byte reset
// token token0.
func NewPool(limit int, cid0, token0 []byte) (*Pool, error) {
	if limit < smallestL || limit > largestL {
		return nil, ErrArg
	}
	if len(cid0) < minCIDLen || len(cid0) > maxCIDLen || len(token0) != tokenLen {
		return nil, ErrArg
	}
	p := &Pool{
		limit:   uint64(limit),
		entries: make(map[uint64]*entry),
		paths:   make(map[uint64]pathSlot),
	}
	e := &entry{seq: 0, cid: append([]byte(nil), cid0...)}
	copy(e.token[:], token0)
	p.entries[0] = e
	return p, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// OnNew processes a NEW_CONNECTION_ID frame advertised by the peer.
//
// Checks run strictly in order: encoding errors, then duplicates/conflicts,
// then the active limit. A rejected call changes no state and leaves no
// tombstone. An exact retransmission (known seq with identical cid and
// token) succeeds without touching any state, including R.
func (p *Pool) OnNew(seq, rpt uint64, cid, token []byte) error {
	if seq > maxSeq || rpt > seq || len(cid) < minCIDLen || len(cid) > maxCIDLen || len(token) != tokenLen {
		return ErrEncoding
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var tok [tokenLen]byte
	copy(tok[:], token)

	if old, ok := p.entries[seq]; ok {
		if bytesEqual(old.cid, cid) && old.token == tok {
			return nil
		}
		return ErrViolation
	}
	for _, e := range p.entries {
		if bytesEqual(e.cid, cid) {
			return ErrViolation
		}
	}

	newR := p.retiredR
	if rpt > newR {
		newR = rpt
	}
	var retiring []uint64
	active := uint64(0)
	for s, e := range p.entries {
		if e.retired {
			continue
		}
		if s < newR {
			retiring = append(retiring, s)
		} else {
			active++
		}
	}
	candidateRetired := seq < newR
	if !candidateRetired {
		active++
	}
	if active > p.limit {
		return ErrLimit
	}

	p.retiredR = newR
	p.entries[seq] = &entry{
		seq:     seq,
		cid:     append([]byte(nil), cid...),
		token:   tok,
		retired: candidateRetired,
	}
	for _, s := range retiring {
		p.entries[s].retired = true
	}

	if candidateRetired {
		retiring = append(retiring, seq)
	}
	sort.Slice(retiring, func(i, j int) bool { return retiring[i] < retiring[j] })
	p.retires = append(p.retires, retiring...)

	p.parkRetiredPaths()
	p.reassignParked()
	return nil
}

func (p *Pool) parkRetiredPaths() {
	for pid, slot := range p.paths {
		if slot.parked {
			continue
		}
		if e, ok := p.entries[slot.seq]; !ok || e.retired {
			slot.parked = true
			p.paths[pid] = slot
		}
	}
}

func (p *Pool) reassignParked() {
	occupied := make(map[uint64]bool)
	var parked []uint64
	for pid, slot := range p.paths {
		if slot.parked {
			parked = append(parked, pid)
		} else {
			occupied[slot.seq] = true
		}
	}
	sort.Slice(parked, func(i, j int) bool { return parked[i] < parked[j] })
	for _, pid := range parked {
		best, found := p.smallestFreeActive(occupied)
		if !found {
			continue
		}
		occupied[best] = true
		p.paths[pid] = pathSlot{seq: best}
	}
}

func (p *Pool) smallestFreeActive(occupied map[uint64]bool) (uint64, bool) {
	best := uint64(0)
	found := false
	for s, e := range p.entries {
		if e.retired || occupied[s] {
			continue
		}
		if !found || s < best {
			best, found = s, true
		}
	}
	return best, found
}

// NewPath registers a path and assigns it the smallest unoccupied active
// entry. The path is not created when the pathID exists or no active entry
// is available.
func (p *Pool) NewPath(pid uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.paths[pid]; ok {
		return ErrArg
	}
	occupied := make(map[uint64]bool)
	for _, slot := range p.paths {
		if !slot.parked {
			occupied[slot.seq] = true
		}
	}
	best, found := p.smallestFreeActive(occupied)
	if !found {
		return ErrNoCID
	}
	p.paths[pid] = pathSlot{seq: best}
	return nil
}

// FreePath removes a path and releases its entry, then reassigns parked
// paths in ascending pathID order.
func (p *Pool) FreePath(pid uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.paths[pid]; !ok {
		return ErrArg
	}
	delete(p.paths, pid)
	p.reassignParked()
	return nil
}

// Retire actively retires a known active entry.
func (p *Pool) Retire(seq uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[seq]
	if !ok || e.retired {
		return ErrArg
	}
	e.retired = true
	p.retires = append(p.retires, seq)
	p.parkRetiredPaths()
	p.reassignParked()
	return nil
}

// TakeRetires returns and clears the retirement queue, in enqueue order.
func (p *Pool) TakeRetires() []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.retires
	p.retires = nil
	return out
}

// PathSlot describes a path assignment.
type PathSlot struct {
	Seq    uint64
	Parked bool
}

// PathSeq returns the entry currently occupied by the path, or its parked
// state.
func (p *Pool) PathSeq(pid uint64) (PathSlot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot, ok := p.paths[pid]
	if !ok {
		return PathSlot{}, ErrArg
	}
	return PathSlot{Seq: slot.seq, Parked: slot.parked}, nil
}

// IsReset reports whether token equals the reset token of some active entry.
func (p *Pool) IsReset(token []byte) bool {
	if len(token) != tokenLen {
		return false
	}
	var tok [tokenLen]byte
	copy(tok[:], token)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if !e.retired && e.token == tok {
			return true
		}
	}
	return false
}

// ActiveCount returns the number of active (non-retired) entries.
func (p *Pool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.entries {
		if !e.retired {
			n++
		}
	}
	return n
}

// R returns the currently effective retire_prior_to value.
func (p *Pool) R() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.retiredR
}

// Entry describes a stored connection ID entry.
type Entry struct {
	CID     []byte
	Token   [tokenLen]byte
	Retired bool
}

// EntryAt returns a copy of the entry bound to seq and whether seq is known.
func (p *Pool) EntryAt(seq uint64) (Entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[seq]
	if !ok {
		return Entry{}, false
	}
	return Entry{CID: append([]byte(nil), e.cid...), Token: e.token, Retired: e.retired}, true
}
