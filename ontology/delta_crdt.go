// Package ontology implements a delta-state CRDT replica for a
// grow-only max map (key -> uint64, pointwise maximum merge).
package ontology

import (
	"errors"
	"sync"
)

// Distinguishable rejection reasons.
var (
	// ErrInvalidCap is returned when the buffer capacity Cap is less than 1.
	ErrInvalidCap = errors.New("invalid cap: must be >= 1")
	// ErrNoPeers is returned when the peer set is empty.
	ErrNoPeers = errors.New("invalid peers: peer set must not be empty")
	// ErrEmptyID is returned when the local id or a peer id is the empty string.
	ErrEmptyID = errors.New("invalid id: ids must be non-empty")
	// ErrSelfInPeers is returned when the peer set contains the local id.
	ErrSelfInPeers = errors.New("invalid peers: peer set must not contain the local id")
	// ErrDuplicatePeer is returned when the peer set contains a duplicate id.
	ErrDuplicatePeer = errors.New("invalid peers: duplicate peer id")

	// ErrUnknownPeer is returned when an operation names a peer that is not in
	// the peer set of the constructor.
	ErrUnknownPeer = errors.New("unknown peer")
	// ErrBufferFull is returned by Apply when an inflation would be recorded
	// but the delta buffer already holds Cap entries.
	ErrBufferFull = errors.New("delta buffer full")
	// ErrInvalidInterval is returned by Receive when a >= b.
	ErrInvalidInterval = errors.New("invalid interval: a must be < b")
	// ErrGap is returned by Receive when a > seen[p], i.e. the causal
	// continuation is missing.
	ErrGap = errors.New("gap: a is ahead of last seen sequence")
	// ErrAckBeyondCursor is returned by Ack when n is greater than the local
	// next-sequence cursor c.
	ErrAckBeyondCursor = errors.New("ack n is greater than cursor c")
)

// State is the pointwise-max map replicated by every Replica.
// A missing key is treated as 0.
type State map[string]uint64

// DeltaGroup is one pointwise-max merged interval of buffered deltas.
type DeltaGroup map[string]uint64

// DeltaMessage is the payload DeltaTo hands to the transport: the half-open
// index interval [A, B) together with the pointwise-max merge of D[A..B).
type DeltaMessage struct {
	A     int
	B     int
	Group DeltaGroup
}

// Replica is one replicated copy of a grow-only max map with delta
// accumulation, per-peer acknowledgements and bounded buffer reclamation.
//
// All methods are safe for concurrent use; their effect is equivalent to
// some serial order.
type Replica struct {
	mu    sync.Mutex
	id    string
	peers map[string]struct{}
	cap   int

	state State              // S: current pointwise-max state
	c     int                // next delta sequence number (starts at 0)
	d     map[int]DeltaGroup // buffered deltas indexed by sequence
	ack   map[string]int     // per-peer acknowledged sequence
	seen  map[string]int     // per-peer last received (contiguous) sequence
}

// New constructs a Replica. It rejects Cap < 1 and illegal peer sets.
func New(id string, peerIDs []string, cap int) (*Replica, error) {
	if cap < 1 {
		return nil, ErrInvalidCap
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	if len(peerIDs) == 0 {
		return nil, ErrNoPeers
	}
	peers := make(map[string]struct{}, len(peerIDs))
	for _, p := range peerIDs {
		if p == "" {
			return nil, ErrEmptyID
		}
		if p == id {
			return nil, ErrSelfInPeers
		}
		if _, dup := peers[p]; dup {
			return nil, ErrDuplicatePeer
		}
		peers[p] = struct{}{}
	}
	r := &Replica{
		id:    id,
		peers: peers,
		cap:   cap,
		state: State{},
		d:     map[int]DeltaGroup{},
		ack:   make(map[string]int, len(peerIDs)),
		seen:  make(map[string]int, len(peerIDs)),
	}
	return r, nil
}

// Apply records an inflation of key to v. It returns (true, nil) when
// v > S[key]; otherwise it returns (false, nil) and consumes no sequence
// number (even when the buffer is full).
func (r *Replica) Apply(key string, v uint64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cur := r.state[key] // missing key reads as the zero value 0
	if v <= cur {
		return false, nil
	}
	if len(r.d) >= r.cap {
		return false, ErrBufferFull
	}
	r.state[key] = v
	r.d[r.c] = DeltaGroup{key: v}
	r.c++
	return true, nil
}

// DeltaTo returns the buffered delta interval not yet acknowledged by peer p,
// i.e. [ack[p], c). The returned group is never aliased to internal state.
func (r *Replica) DeltaTo(p string) (DeltaMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.knownPeer(p) {
		return DeltaMessage{}, ErrUnknownPeer
	}
	a := r.ack[p]
	b := r.c
	if a == b {
		return DeltaMessage{}, nil
	}
	group := DeltaGroup{}
	for i := a; i < b; i++ {
		mergeInto(group, r.d[i])
	}
	return DeltaMessage{A: a, B: b, Group: group}, nil
}

// Receive causally merges a message from peer p. It returns the current
// value of seen[p] (on both success and stale duplicate delivery).
// Error precedence: unknown peer, invalid interval (a >= b), gap.
func (r *Replica) Receive(p string, a, b int, group DeltaGroup) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.knownPeer(p) {
		return 0, ErrUnknownPeer
	}
	if a >= b {
		return 0, ErrInvalidInterval
	}
	s := r.seen[p]
	if a > s {
		// Causal gap: sequences in [s, a) have never been received.
		return s, ErrGap
	}
	if b <= s {
		// Stale duplicate of an already covered interval: merge nothing and
		// leave seen[p] untouched.
		return s, nil
	}
	// a <= s < b: merge the whole group (pointwise maximum is idempotent, so
	// re-merging the already-seen prefix is harmless) and advance seen to b.
	mergeInto(r.state, group)
	r.seen[p] = b
	return b, nil
}

// Ack records p's acknowledgement up to sequence n and reclaims buffered
// entries whose index is below the minimum ack of all peers.
func (r *Replica) Ack(p string, n int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.knownPeer(p) {
		return ErrUnknownPeer
	}
	if n > r.c {
		return ErrAckBeyondCursor
	}
	if n > r.ack[p] {
		r.ack[p] = n
	}
	// Smaller (rollback) acks are ignored and are not an error.
	minAck := r.c
	for peer := range r.peers {
		if r.ack[peer] < minAck {
			minAck = r.ack[peer]
		}
	}
	// Reclaim every entry strictly below the global minimum; an entry whose
	// index equals the minimum must be retained.
	for i := range r.d {
		if i < minAck {
			delete(r.d, i)
		}
	}
	return nil
}

// Snapshot returns a defensive copy of the current state.
func (r *Replica) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(State, len(r.state))
	for k, v := range r.state {
		out[k] = v
	}
	return out
}

// ID reports the local replica identifier.
func (r *Replica) ID() string { return r.id }

// Cursor reports the next delta sequence number c.
func (r *Replica) Cursor() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c
}

// AckOf reports p's current acknowledgement.
func (r *Replica) AckOf(p string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.knownPeer(p) {
		return 0, ErrUnknownPeer
	}
	return r.ack[p], nil
}

// SeenOf reports p's last contiguous received sequence.
func (r *Replica) SeenOf(p string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.knownPeer(p) {
		return 0, ErrUnknownPeer
	}
	return r.seen[p], nil
}

// BufferLen reports the number of retained delta entries; the invariant
// |D| == c - min_p ack[p] always holds.
func (r *Replica) BufferLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.d)
}

func (r *Replica) knownPeer(p string) bool {
	_, ok := r.peers[p]
	return ok
}

// mergeInto pointwise-merges src into dst (dst[k] = max(dst[k], src[k])).
func mergeInto(dst, src map[string]uint64) {
	for k, v := range src {
		if v > dst[k] {
			dst[k] = v
		}
	}
}
