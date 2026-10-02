// Package lessor implements an etcd-style lease manager with TTL grants,
// renewals, key attachment, rate-limited expiry revocation, checkpointing and
// leader/follower failover semantics.
package lessor

import (
	"sort"
	"sync"
)

// Config holds the lessor construction parameters.
type Config struct {
	MinTTL int64
	MaxTTL int64
	E      int64
	R      int64
	Kmax   int
}

// Revocation is one lease removed by Tick, carrying its formerly attached keys
// in ascending order.
type Revocation struct {
	ID   int64
	Keys []string
}

// Sentinel errors. Validation precedence is: argument errors, time errors,
// role errors, then per-operation semantic errors.
var (
	ErrInvalidConfig = errInvalidConfig{}
	ErrInvalidID     = errInvalidID{}
	ErrEmptyKey      = errEmptyKey{}
	ErrInvalidTTL    = errInvalidTTL{}
	ErrInvalidTime   = errInvalidTime{}
	ErrNotLeader     = errNotLeader{}
	ErrWrongRole     = errWrongRole{}
	ErrLeaseExists   = errLeaseExists{}
	ErrNoLease       = errNoLease{}
	ErrExpired       = errExpired{}
	ErrLeaseFull     = errLeaseFull{}
)

type (
	errInvalidConfig struct{}
	errInvalidID     struct{}
	errEmptyKey      struct{}
	errInvalidTTL    struct{}
	errInvalidTime   struct{}
	errNotLeader     struct{}
	errWrongRole     struct{}
	errLeaseExists   struct{}
	errNoLease       struct{}
	errExpired       struct{}
	errLeaseFull     struct{}
)

func (errInvalidConfig) Error() string { return "lessor: invalid configuration" }
func (errInvalidID) Error() string     { return "lessor: lease id must be a positive integer" }
func (errEmptyKey) Error() string      { return "lessor: key must not be empty" }
func (errInvalidTTL) Error() string    { return "lessor: ttl out of range" }
func (errInvalidTime) Error() string   { return "lessor: now out of range or before clock watermark" }
func (errNotLeader) Error() string     { return "lessor: operation requires leader role" }
func (errWrongRole) Error() string     { return "lessor: role transition invalid for current role" }
func (errLeaseExists) Error() string   { return "lessor: lease already exists" }
func (errNoLease) Error() string       { return "lessor: lease does not exist" }
func (errExpired) Error() string       { return "lessor: lease has expired" }
func (errLeaseFull) Error() string     { return "lessor: lease has reached Kmax attached keys" }

// lease is the internal record of one granted lease.
type lease struct {
	id int64
	// g is the effective TTL: max(requested ttl, MinTTL).
	g int64
	// sv is the checkpointed remaining lifetime; 0 means "no checkpoint".
	sv int64
	// x is the expiry instant; only meaningful while leader.
	x int64
	// keys are the attached keys, kept sorted and unique.
	keys []string
	// idx is the position in the expiry min-heap, or -1 when not queued.
	idx int
}

// Lessor is the concurrency-safe lease manager. The zero value is not usable;
// construct it with New.
type Lessor struct {
	mu sync.Mutex

	cfg Config

	// leader is the current role; a freshly constructed lessor is follower.
	leader bool

	// clock is the monotonic watermark T.
	clock int64

	// leases stores every live lease, including expired-but-backlogged ones.
	leases map[int64]*lease

	// keyOwner maps each non-empty key to the lease it is attached to.
	keyOwner map[string]int64

	// heap is the expiry min-heap ordered by (x, id). It is always in exact
	// agreement with leases: no stale entries are tolerated.
	heap []*lease

	// tickPeeks counts the number of times Tick inspects heap[0]. It proves
	// that each Tick examines at most revocations+1 heap tops.
	tickPeeks int64
}

// New constructs a follower lessor. It returns ErrInvalidConfig when any
// construction parameter is out of its legal range.
func New(cfg Config) (*Lessor, error) {
	if cfg.MinTTL < 1 || cfg.MinTTL > 1_000_000 ||
		cfg.MaxTTL < cfg.MinTTL || cfg.MaxTTL > 1_000_000_000 ||
		cfg.E < 0 || cfg.E > 1_000_000_000 ||
		cfg.R < 1 || cfg.R > 1_000_000 ||
		cfg.Kmax < 1 || cfg.Kmax > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Lessor{
		cfg:      cfg,
		leases:   make(map[int64]*lease),
		keyOwner: make(map[string]int64),
		heap:     make([]*lease, 0),
	}, nil
}

// Grant creates a lease on the leader.
func (l *Lessor) Grant(id, ttl, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id < 1 {
		return ErrInvalidID
	}
	if ttl < 1 || ttl > l.cfg.MaxTTL {
		return ErrInvalidTTL
	}
	if !l.validTime(now) {
		return ErrInvalidTime
	}
	if !l.leader {
		return ErrNotLeader
	}
	if _, ok := l.leases[id]; ok {
		return ErrLeaseExists
	}

	g := ttl
	if g < l.cfg.MinTTL {
		g = l.cfg.MinTTL
	}
	ls := &lease{id: id, g: g, x: now + g, idx: -1}
	l.leases[id] = ls
	l.heapPush(ls)
	l.clock = now
	return nil
}

// Renew extends an existing lease and returns its effective TTL g.
func (l *Lessor) Renew(id, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id < 1 {
		return 0, ErrInvalidID
	}
	if !l.validTime(now) {
		return 0, ErrInvalidTime
	}
	if !l.leader {
		return 0, ErrNotLeader
	}
	ls, ok := l.leases[id]
	if !ok {
		return 0, ErrNoLease
	}
	// x == now is already expired. The lease stays in the backlog.
	if ls.x <= now {
		return 0, ErrExpired
	}

	ls.x = now + ls.g
	ls.sv = 0
	l.heapFix(ls)
	l.clock = now
	return ls.g, nil
}

// Attach attaches a key to a lease, moving it from any previous owner.
func (l *Lessor) Attach(key string, id, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id < 1 {
		return ErrInvalidID
	}
	if key == "" {
		return ErrEmptyKey
	}
	if !l.validTime(now) {
		return ErrInvalidTime
	}
	if !l.leader {
		return ErrNotLeader
	}
	target, ok := l.leases[id]
	if !ok {
		return ErrNoLease
	}
	if target.x <= now {
		return ErrExpired
	}

	if owner, has := l.keyOwner[key]; has {
		// Already attached to the target lease: idempotent no-op.
		if owner == id {
			l.clock = now
			return nil
		}
		// Capacity is judged before any detachment, so a rejected attach
		// leaves the key on its current lease.
		if len(target.keys) >= l.cfg.Kmax {
			return ErrLeaseFull
		}
		old := l.leases[owner]
		pos := sort.SearchStrings(old.keys, key)
		old.keys = append(old.keys[:pos], old.keys[pos+1:]...)
	} else {
		if len(target.keys) >= l.cfg.Kmax {
			return ErrLeaseFull
		}
	}

	pos := sort.SearchStrings(target.keys, key)
	target.keys = append(target.keys, "")
	copy(target.keys[pos+1:], target.keys[pos:])
	target.keys[pos] = key
	l.keyOwner[key] = id
	l.clock = now
	return nil
}

// Tick revokes up to R expired leases in (x, id) order.
func (l *Lessor) Tick(now int64) ([]Revocation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.validTime(now) {
		return nil, ErrInvalidTime
	}
	if !l.leader {
		return nil, ErrNotLeader
	}

	out := []Revocation{}
	for int64(len(out)) < l.cfg.R {
		if len(l.heap) == 0 {
			break
		}
		// One heap-top inspection per iteration; the last, non-expired top
		// is the single "+1" inspection.
		l.tickPeeks++
		top := l.heap[0]
		if top.x > now {
			break
		}
		keys := append([]string(nil), top.keys...)
		l.heapRemove(top)
		l.deleteLease(top)
		out = append(out, Revocation{ID: top.id, Keys: keys})
	}
	l.clock = now
	return out, nil
}

// Revoke removes one lease regardless of expiry; it does not consume the Tick
// budget R.
func (l *Lessor) Revoke(id, now int64) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id < 1 {
		return nil, ErrInvalidID
	}
	if !l.validTime(now) {
		return nil, ErrInvalidTime
	}
	if !l.leader {
		return nil, ErrNotLeader
	}
	ls, ok := l.leases[id]
	if !ok {
		return nil, ErrNoLease
	}

	keys := append([]string(nil), ls.keys...)
	l.heapRemove(ls)
	l.deleteLease(ls)
	l.clock = now
	return keys, nil
}

// Checkpoint writes sv = x - now for every not-yet-expired lease.
func (l *Lessor) Checkpoint(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.validTime(now) {
		return ErrInvalidTime
	}
	if !l.leader {
		return ErrNotLeader
	}

	for _, ls := range l.leases {
		if ls.x > now {
			ls.sv = ls.x - now
		}
	}
	l.clock = now
	return nil
}

// Demote turns a leader into a follower. Lease data is left untouched.
func (l *Lessor) Demote(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.validTime(now) {
		return ErrInvalidTime
	}
	if !l.leader {
		return ErrWrongRole
	}

	l.leader = false
	l.clock = now
	return nil
}

// Promote turns a follower into a leader and recomputes every expiry instant.
func (l *Lessor) Promote(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.validTime(now) {
		return ErrInvalidTime
	}
	if l.leader {
		return ErrWrongRole
	}

	l.leader = true
	l.heap = l.heap[:0]
	for _, ls := range l.leases {
		rem := ls.g
		if ls.sv > 0 {
			rem = ls.sv
		}
		ls.x = now + l.cfg.E + rem
		l.heapPush(ls)
	}
	l.clock = now
	return nil
}

// TTL reports the remaining lifetime without changing the clock watermark.
func (l *Lessor) TTL(id, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id < 1 {
		return 0, ErrInvalidID
	}
	if !l.validTime(now) {
		return 0, ErrInvalidTime
	}
	ls, ok := l.leases[id]
	if !ok {
		return 0, ErrNoLease
	}

	if l.leader {
		if ls.x > now {
			return ls.x - now, nil
		}
		return 0, nil
	}
	if ls.sv > 0 {
		return ls.sv, nil
	}
	return ls.g, nil
}

// TickPeeks returns the cumulative number of heap-top inspections performed by
// Tick calls.
func (l *Lessor) TickPeeks() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tickPeeks
}

// validTime reports whether now is within range and not behind the watermark.
func (l *Lessor) validTime(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000_000 && now >= l.clock
}

// deleteLease removes a lease from the map and releases its key attachments.
// The caller must already have removed it from the heap.
func (l *Lessor) deleteLease(ls *lease) {
	for _, key := range ls.keys {
		delete(l.keyOwner, key)
	}
	ls.keys = nil
	delete(l.leases, ls.id)
}

// heapLess orders heap entries by (x, id) ascending.
func (l *Lessor) heapLess(a, b *lease) bool {
	return a.x < b.x || (a.x == b.x && a.id < b.id)
}

// heapPush appends a lease and sifts it up.
func (l *Lessor) heapPush(ls *lease) {
	ls.idx = len(l.heap)
	l.heap = append(l.heap, ls)
	l.siftUp(ls.idx)
}

// heapRemove deletes an arbitrary lease from the heap without leaving stale
// entries.
func (l *Lessor) heapRemove(ls *lease) {
	pos := ls.idx
	if pos < 0 {
		return
	}
	last := len(l.heap) - 1
	if pos != last {
		l.heapSwap(pos, last)
	}
	l.heap[last].idx = -1
	l.heap = l.heap[:last]
	if pos < len(l.heap) {
		l.siftUp(pos)
		l.siftDown(pos)
	}
}

// heapFix restores heap order after a lease's x changes.
func (l *Lessor) heapFix(ls *lease) {
	if ls.idx < 0 {
		return
	}
	l.siftUp(ls.idx)
	l.siftDown(ls.idx)
}

func (l *Lessor) heapSwap(i, j int) {
	l.heap[i], l.heap[j] = l.heap[j], l.heap[i]
	l.heap[i].idx = i
	l.heap[j].idx = j
}

func (l *Lessor) siftUp(pos int) {
	for pos > 0 {
		parent := (pos - 1) / 2
		if !l.heapLess(l.heap[pos], l.heap[parent]) {
			return
		}
		l.heapSwap(pos, parent)
		pos = parent
	}
}

func (l *Lessor) siftDown(pos int) {
	n := len(l.heap)
	for {
		left := 2*pos + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && l.heapLess(l.heap[right], l.heap[left]) {
			smallest = right
		}
		if !l.heapLess(l.heap[smallest], l.heap[pos]) {
			return
		}
		l.heapSwap(pos, smallest)
		pos = smallest
	}
}
