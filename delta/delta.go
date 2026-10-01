// Package delta implements a delta-state CRDT (delta-CRDT) replica for a
// key -> unsigned integer max-map, with per-peer acknowledgement tracking,
// sequenced delta buffering, interval-based delta dissemination and
// causal merge of received delta groups.
package delta

import (
	"errors"
	"sync"
)

// Distinguishable failure reasons returned by Replica operations.
var (
	ErrInvalidCap       = errors.New("delta: cap must be >= 1")
	ErrEmptyReplicaID   = errors.New("delta: replica id must be non-empty")
	ErrEmptyPeerSet     = errors.New("delta: peer set must be non-empty")
	ErrEmptyPeerID      = errors.New("delta: peer id must be non-empty")
	ErrDuplicatePeer    = errors.New("delta: peer ids must be distinct")
	ErrPeerIsSelf       = errors.New("delta: peer set must not contain the replica id")
	ErrBufferFull       = errors.New("delta: delta buffer reached cap")
	ErrUnknownPeer      = errors.New("delta: unknown peer")
	ErrInvalidInterval  = errors.New("delta: invalid interval, require a < b")
	ErrGap              = errors.New("delta: causal gap, a is ahead of seen")
	ErrAckBeyondCounter = errors.New("delta: ack ahead of local counter")
)

// Replica is a single delta-CRDT replica of a key -> uint64 max-map.
type Replica struct {
	mu    sync.Mutex
	id    string
	peers map[string]struct{}
	cap   int
	state map[string]uint64
	c     uint64
	buf   map[uint64]map[string]uint64
	ack   map[string]uint64
	seen  map[string]uint64
}

// NewReplica builds a replica with the given id, peer set and buffer cap.
func NewReplica(id string, peers []string, cap int) (*Replica, error) {
	if cap < 1 {
		return nil, ErrInvalidCap
	}
	if id == "" {
		return nil, ErrEmptyReplicaID
	}
	if len(peers) == 0 {
		return nil, ErrEmptyPeerSet
	}
	peerSet := make(map[string]struct{}, len(peers))
	for _, p := range peers {
		if p == "" {
			return nil, ErrEmptyPeerID
		}
		if p == id {
			return nil, ErrPeerIsSelf
		}
		if _, dup := peerSet[p]; dup {
			return nil, ErrDuplicatePeer
		}
		peerSet[p] = struct{}{}
	}
	ack := make(map[string]uint64, len(peerSet))
	seen := make(map[string]uint64, len(peerSet))
	for p := range peerSet {
		ack[p] = 0
		seen[p] = 0
	}
	return &Replica{
		id:    id,
		peers: peerSet,
		cap:   cap,
		state: make(map[string]uint64),
		buf:   make(map[uint64]map[string]uint64),
		ack:   ack,
		seen:  seen,
	}, nil
}

// Apply records key=v if v inflates the current value of key.
// A non-inflating apply is a no-op: it consumes no sequence number and
// never fails, even when the buffer is full.
func (r *Replica) Apply(key string, v uint64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v <= r.state[key] {
		return false, nil
	}
	if len(r.buf) >= r.cap {
		return false, ErrBufferFull
	}
	r.state[key] = v
	r.buf[r.c] = map[string]uint64{key: v}
	r.c++
	return true, nil
}

// DeltaTo returns the delta group for the interval [ack[p], c) addressed to p.
// The returned group is a fresh map and never aliases internal state.
func (r *Replica) DeltaTo(p string) (a, b uint64, group map[string]uint64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.peers[p]; !ok {
		return 0, 0, nil, ErrUnknownPeer
	}
	a, b = r.ack[p], r.c
	if a == b {
		return a, b, nil, nil
	}
	group = make(map[string]uint64)
	for seq := a; seq < b; seq++ {
		for k, v := range r.buf[seq] {
			if v > group[k] {
				group[k] = v
			}
		}
	}
	return a, b, group, nil
}

// Receive causally merges a delta group from p covering interval [a, b).
// It returns the current seen[p] on success and on stale duplicates.
// Checks are ordered: unknown peer, invalid interval, causal gap.
func (r *Replica) Receive(p string, a, b uint64, group map[string]uint64) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.peers[p]; !ok {
		return 0, ErrUnknownPeer
	}
	if a >= b {
		return 0, ErrInvalidInterval
	}
	if a > r.seen[p] {
		return 0, ErrGap
	}
	if b <= r.seen[p] {
		return r.seen[p], nil
	}
	for k, v := range group {
		if v > r.state[k] {
			r.state[k] = v
		}
	}
	r.seen[p] = b
	return b, nil
}

// Ack records that peer p has received everything up to sequence n.
// Regressing acks are ignored without error. Afterwards every buffered
// entry with sequence below the minimum ack over all peers is reclaimed.
func (r *Replica) Ack(p string, n uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.peers[p]; !ok {
		return ErrUnknownPeer
	}
	if n > r.c {
		return ErrAckBeyondCounter
	}
	if n > r.ack[p] {
		r.ack[p] = n
	}
	min := r.ack[p]
	for _, v := range r.ack {
		if v < min {
			min = v
		}
	}
	for seq := range r.buf {
		if seq < min {
			delete(r.buf, seq)
		}
	}
	return nil
}

// Snapshot returns a copy of the current state map.
func (r *Replica) Snapshot() map[string]uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]uint64, len(r.state))
	for k, v := range r.state {
		out[k] = v
	}
	return out
}

// Counter returns the next delta sequence number c.
func (r *Replica) Counter() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c
}

// AckOf returns the current ack cursor for peer p.
func (r *Replica) AckOf(p string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.ack[p]
	return v, ok
}

// SeenOf returns the current seen cursor for peer p.
func (r *Replica) SeenOf(p string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.seen[p]
	return v, ok
}

// Buffered returns the number of entries currently held in the delta buffer.
func (r *Replica) Buffered() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf)
}
