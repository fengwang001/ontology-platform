package registry

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/acl"
	"ontology/policy"
)

const (
	stRunning = iota
	stCompleted
	stFailed
	stCancelled
	stTerminated
	maxNow    = int64(1_000_000_000_000_000)
	maxRetain = int64(1_000_000_000)
)

var (
	ErrInvalid    = errors.New("registry: invalid argument")
	ErrClock      = errors.New("registry: clock moved backwards")
	ErrRunning    = errors.New("registry: an instance is running")
	ErrDenied     = errors.New("registry: terminate denied")
	ErrReuse      = errors.New("registry: reuse rejected")
	ErrCapacity   = errors.New("registry: capacity exhausted")
	ErrNotFound   = errors.New("registry: id not found")
	ErrStale      = errors.New("registry: stale run number")
	ErrNotRunning = errors.New("registry: instance not running")
)

type entry struct {
	run, ended int64
	owner      []byte
	state      int
	heapSeq    int64
}

type heapItem struct {
	expiry int64
	id     string
	seq    int64
}
type expiryHeap []heapItem

func (h expiryHeap) Len() int { return len(h) }
func (h expiryHeap) Less(i, j int) bool {
	return h[i].expiry < h[j].expiry ||
		(h[i].expiry == h[j].expiry && h[i].seq < h[j].seq)
}
func (h expiryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)   { *h = append(*h, x.(heapItem)) }
func (h *expiryHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

type Registry struct {
	acl.ACL
	mu        sync.Mutex
	retention int64
	capacity  int
	clock     int64
	nextRun   int64
	nextSeq   int64
	recs      map[string]*entry
	heap      expiryHeap
	lastProbe int
}

func New(R int64, N int) (*Registry, error) {
	if R < 1 || R > maxRetain || N < 1 || N > 1_000_000 {
		return nil, ErrInvalid
	}
	r := &Registry{ACL: *acl.New(), retention: R, capacity: N, recs: map[string]*entry{}, heap: expiryHeap{}}
	return r, nil
}

func (r *Registry) Start(id, p []byte, reuse, conflict int, now int64) (int64, error) {
	if len(id) == 0 || len(p) == 0 || now < 0 || now > maxNow ||
		reuse < 0 || reuse > int(policy.Reject) || conflict < 0 || conflict > int(policy.Terminate) {
		return 0, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.clock {
		return 0, ErrClock
	}
	r.purge(now)
	key := string(id)
	cur := r.recs[key]
	var rec *policy.Record
	if cur != nil {
		rec = &policy.Record{Run: cur.run, Owner: cur.owner, State: policy.Status(cur.state)}
	}
	switch policy.Decide(rec, policy.Reuse(reuse), policy.Conflict(conflict)) {
	case policy.ActionRejectRunning:
		return 0, ErrRunning
	case policy.ActionReturnExisting:
		r.clock = now
		return cur.run, nil
	case policy.ActionTerminateAndCreate:
		if !r.CanTerminate(p, cur.owner) {
			return 0, ErrDenied
		}
		cur.state, cur.ended = stTerminated, now
		cur.heapSeq = r.pushItem(key, now+r.retention)
		return r.createLocked(key, p, now), nil
	case policy.ActionReuse:
		return r.createLocked(key, p, now), nil
	case policy.ActionDenyReuse:
		return 0, ErrReuse
	case policy.ActionCreate:
		if len(r.recs) >= r.capacity {
			return 0, ErrCapacity
		}
		return r.createLocked(key, p, now), nil
	default:
		return 0, ErrInvalid
	}
}

func (r *Registry) Finish(id []byte, run int64, state int, now int64) error {
	if len(id) == 0 || state < stCompleted || state > stCancelled || now < 0 || now > maxNow {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.clock {
		return ErrClock
	}
	r.purge(now)
	cur := r.recs[string(id)]
	switch {
	case cur == nil:
		return ErrNotFound
	case cur.run != run:
		return ErrStale
	case cur.state != stRunning:
		return ErrNotRunning
	}
	cur.state, cur.ended = state, now
	cur.heapSeq = r.pushItem(string(id), now+r.retention)
	r.clock = now
	return nil
}

func (r *Registry) Count(now int64) (int, error) {
	if now < 0 || now > maxNow {
		return 0, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.clock {
		return 0, ErrClock
	}
	r.purge(now)
	return len(r.recs), nil
}

func (r *Registry) purgeProbes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.lastProbe
	r.lastProbe = 0
	return n
}

func (r *Registry) pushItem(id string, expiry int64) int64 {
	r.nextSeq++
	heap.Push(&r.heap, heapItem{expiry: expiry, id: id, seq: r.nextSeq})
	return r.nextSeq
}

func (r *Registry) purge(now int64) {
	probed := 0
	for r.heap.Len() > 0 {
		top := r.heap[0]
		probed++
		if top.expiry > now {
			break
		}
		heap.Pop(&r.heap)
		if cur, ok := r.recs[top.id]; ok && cur.heapSeq == top.seq {
			delete(r.recs, top.id)
		}
	}
	r.lastProbe += probed
}

func (r *Registry) createLocked(key string, p []byte, now int64) int64 {
	r.nextRun++
	r.recs[key] = &entry{run: r.nextRun, owner: append([]byte(nil), p...), state: stRunning}
	r.clock = now
	return r.nextRun
}
