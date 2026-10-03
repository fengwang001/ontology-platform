// Package cidpool implements a QUIC-style connection ID pool manager.
//
// The pool receives connection IDs issued by the peer, retires them in
// batches according to retire_prior_to, enforces the active-count limit
// and conflict rules, and lets multiple paths each occupy a distinct
// active connection ID. All operations are safe for concurrent use and
// behave as if executed in some serial order; replaying the same call
// sequence reproduces the exact same retire queue and path assignments.
package cidpool

import (
	"bytes"
	"fmt"
	"sort"
	"sync"
)

// ErrCode classifies why an operation was rejected.
type ErrCode int

const (
	// ErrEncoding: retire_prior_to > seq, or cid length outside 1..20.
	ErrEncoding ErrCode = iota + 1
	// ErrViolation: seq known with different (cid, token), or cid
	// already used by another known entry (including retired ones).
	ErrViolation
	// ErrLimit: applying the frame would exceed the active-count limit.
	ErrLimit
	// ErrArg: invalid argument (duplicate path, unknown path/seq).
	ErrArg
	// ErrNoCID: no free active connection ID available for a new path.
	ErrNoCID
)

func (c ErrCode) String() string {
	switch c {
	case ErrEncoding:
		return "encoding error"
	case ErrViolation:
		return "protocol violation"
	case ErrLimit:
		return "active limit exceeded"
	case ErrArg:
		return "invalid argument"
	case ErrNoCID:
		return "no connection id available"
	}
	return "unknown error"
}

// Error is the single error type returned by Pool operations; inspect
// Code to distinguish rejection causes.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("cidpool: %s: %s", e.Code, e.Msg)
}

// PathState describes what PathSeq reports for a path.
type PathState int

const (
	// PathMissing: no such path.
	PathMissing PathState = iota
	// PathShelved: path exists but currently occupies no connection ID.
	PathShelved
	// PathActive: path occupies the returned sequence number.
	PathActive
)

func (s PathState) String() string {
	switch s {
	case PathMissing:
		return "missing"
	case PathShelved:
		return "shelved"
	case PathActive:
		return "active"
	}
	return "unknown"
}

type entry struct {
	cid     []byte
	token   [16]byte
	retired bool
}

type pathSlot struct {
	seq     uint64
	shelved bool
}

// Pool is a connection ID pool manager. The zero value is not usable;
// construct with New.
type Pool struct {
	mu      sync.Mutex
	limit   int
	entries map[uint64]*entry
	r       uint64 // effective retire_prior_to
	queue   []uint64
	paths   map[uint64]pathSlot
}

// New creates a pool with active limit (2..16), the initial connection
// ID cid0 (sequence 0, 1..20 bytes) and its stateless reset token.
func New(limit int, cid0 []byte, token0 [16]byte) (*Pool, error) {
	if limit < 2 || limit > 16 {
		return nil, &Error{Code: ErrArg, Msg: fmt.Sprintf("active limit %d out of range [2,16]", limit)}
	}
	if len(cid0) < 1 || len(cid0) > 20 {
		return nil, &Error{Code: ErrArg, Msg: fmt.Sprintf("initial cid length %d out of range [1,20]", len(cid0))}
	}
	p := &Pool{
		limit:   limit,
		entries: make(map[uint64]*entry),
		paths:   make(map[uint64]pathSlot),
	}
	p.entries[0] = &entry{cid: append([]byte(nil), cid0...), token: token0}
	return p, nil
}

// OnNew processes a peer-advertised connection ID.
//
// Evaluation order (first match wins, rejections change nothing):
//  1. encoding: rpt > seq, or cid length outside 1..20 -> ErrEncoding;
//  2. conflict: seq known with identical (cid, token) -> duplicate,
//     success with no state change; seq known with different
//     (cid, token), or cid equal to any known entry's cid (retired
//     tombstones included) -> ErrViolation;
//  3. deduction: R' = max(R, rpt); insert the new entry; every entry
//     with seq < R' is retired (the new entry retires immediately if
//     seq < R'); if the resulting active count exceeds the limit ->
//     ErrLimit.
//
// On commit R becomes R', newly retired sequences are appended to the
// retire queue in ascending order, paths occupying retired sequences
// are shelved, and all shelved paths are reassigned in ascending
// pathID order to the smallest unoccupied active sequence.
func (p *Pool) OnNew(seq, rpt uint64, cid []byte, token [16]byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// (1) encoding errors
	if rpt > seq {
		return &Error{Code: ErrEncoding, Msg: fmt.Sprintf("retire_prior_to %d greater than sequence %d", rpt, seq)}
	}
	if len(cid) < 1 || len(cid) > 20 {
		return &Error{Code: ErrEncoding, Msg: fmt.Sprintf("cid length %d out of range [1,20]", len(cid))}
	}

	// (2) duplicates and conflicts
	if e, ok := p.entries[seq]; ok {
		if bytes.Equal(e.cid, cid) && e.token == token {
			return nil // exact duplicate: no state change, not even R
		}
		return &Error{Code: ErrViolation, Msg: fmt.Sprintf("sequence %d already known with different cid/token", seq)}
	}
	for s, e := range p.entries {
		if bytes.Equal(e.cid, cid) {
			return &Error{Code: ErrViolation, Msg: fmt.Sprintf("cid already used by sequence %d", s)}
		}
	}

	// (3) deduction against the tentative R'
	r2 := p.r
	if rpt > r2 {
		r2 = rpt
	}
	active := 0
	for s, e := range p.entries {
		if !e.retired && s >= r2 {
			active++
		}
	}
	if seq >= r2 {
		active++ // the new entry itself survives iff seq >= R'
	}
	if active > p.limit {
		return &Error{Code: ErrLimit, Msg: fmt.Sprintf("active count %d would exceed limit %d", active, p.limit)}
	}

	// commit
	p.r = r2
	e := &entry{cid: append([]byte(nil), cid...), token: token, retired: seq < r2}
	p.entries[seq] = e
	var newly []uint64
	for s, en := range p.entries {
		if !en.retired && s < r2 {
			en.retired = true
			newly = append(newly, s)
		}
	}
	if e.retired {
		newly = append(newly, seq)
	}
	sort.Slice(newly, func(i, j int) bool { return newly[i] < newly[j] })
	p.queue = append(p.queue, newly...)
	p.shelveLocked(newly)
	p.reassignLocked()
	return nil
}

// NewPath creates a path occupying the smallest free active sequence.
func (p *Pool) NewPath(pid uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.paths[pid]; ok {
		return &Error{Code: ErrArg, Msg: fmt.Sprintf("path %d already exists", pid)}
	}
	seq, ok := p.smallestFreeLocked()
	if !ok {
		return &Error{Code: ErrNoCID, Msg: "no free active connection id"}
	}
	p.paths[pid] = pathSlot{seq: seq}
	return nil
}

// FreePath removes a path, releasing its connection ID, and reassigns
// remaining shelved paths.
func (p *Pool) FreePath(pid uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.paths[pid]; !ok {
		return &Error{Code: ErrArg, Msg: fmt.Sprintf("path %d does not exist", pid)}
	}
	delete(p.paths, pid)
	p.reassignLocked()
	return nil
}

// Retire locally retires a known active sequence.
func (p *Pool) Retire(seq uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[seq]
	if !ok || e.retired {
		return &Error{Code: ErrArg, Msg: fmt.Sprintf("sequence %d is not a known active entry", seq)}
	}
	e.retired = true
	p.queue = append(p.queue, seq)
	p.shelveLocked([]uint64{seq})
	p.reassignLocked()
	return nil
}

// TakeRetires returns and clears the pending retire queue, in enqueue
// order.
func (p *Pool) TakeRetires() []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]uint64(nil), p.queue...)
	p.queue = nil
	return out
}

// PathSeq reports the sequence occupied by pid, or its shelved/missing
// state.
func (p *Pool) PathSeq(pid uint64) (uint64, PathState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot, ok := p.paths[pid]
	if !ok {
		return 0, PathMissing
	}
	if slot.shelved {
		return 0, PathShelved
	}
	return slot.seq, PathActive
}

// IsReset reports whether token matches some active entry's stateless
// reset token.
func (p *Pool) IsReset(token [16]byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if !e.retired && e.token == token {
			return true
		}
	}
	return false
}

// ActiveCount returns the number of unretired entries.
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

// shelveLocked shelves every path occupying one of seqs.
func (p *Pool) shelveLocked(seqs []uint64) {
	gone := make(map[uint64]bool, len(seqs))
	for _, s := range seqs {
		gone[s] = true
	}
	for pid, slot := range p.paths {
		if !slot.shelved && gone[slot.seq] {
			p.paths[pid] = pathSlot{shelved: true}
		}
	}
}

// reassignLocked assigns every shelved path, in ascending pathID
// order, the smallest active sequence not occupied by any path; paths
// remain shelved when none is available.
func (p *Pool) reassignLocked() {
	var shelved []uint64
	for pid, slot := range p.paths {
		if slot.shelved {
			shelved = append(shelved, pid)
		}
	}
	sort.Slice(shelved, func(i, j int) bool { return shelved[i] < shelved[j] })
	for _, pid := range shelved {
		seq, ok := p.smallestFreeLocked()
		if !ok {
			return
		}
		p.paths[pid] = pathSlot{seq: seq}
	}
}

// smallestFreeLocked returns the smallest active sequence not occupied
// by any path.
func (p *Pool) smallestFreeLocked() (uint64, bool) {
	used := make(map[uint64]bool, len(p.paths))
	for _, slot := range p.paths {
		if !slot.shelved {
			used[slot.seq] = true
		}
	}
	var best uint64
	found := false
	for s, e := range p.entries {
		if e.retired || used[s] {
			continue
		}
		if !found || s < best {
			best, found = s, true
		}
	}
	return best, found
}
