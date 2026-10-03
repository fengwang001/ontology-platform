// Package gateway coordinates per-tenant token quotas and a bounded
// priority queue. A single mutex serializes all operations, so concurrent
// calls are equivalent to some serial order and replays are deterministic.
package gateway

import (
	"errors"
	"sync"

	"ontology/queue"
	"ontology/quota"
)

const (
	MaxNow    = 1_000_000_000_000
	MaxBudget = 1_000_000_000_000
)

var (
	ErrInvalidParam   = errors.New("gateway: invalid parameter")
	ErrClockBackward  = errors.New("gateway: clock moved backward")
	ErrUnknownTenant  = errors.New("gateway: unknown tenant")
	ErrPrioNotAllowed = errors.New("gateway: priority not allowed for tenant")
	ErrQuotaExceeded  = errors.New("gateway: tenant quota exceeded")
	ErrQueueFull      = errors.New("gateway: queue full")
)

// TenantStats tracks byte flows per priority; for each tenant and each
// priority the invariant Accepted = Evicted + Drained + Queued holds.
type TenantStats struct {
	AcceptedBytes [queue.NumPrio]int64
	EvictedBytes  [queue.NumPrio]int64
	DrainedBytes  [queue.NumPrio]int64
	QueuedBytes   [queue.NumPrio]int64
}

// IngestResult reports the assigned sequence number and the records
// evicted to make room, in eviction order.
type IngestResult struct {
	Seq     uint64
	Evicted []queue.Record
}

type Gateway struct {
	mu     sync.Mutex
	reg    *quota.Registry
	q      *queue.Queue
	maxNow int64 // largest accepted clock value; -1 before any accepted op
	stats  map[string]*TenantStats
}

func New(capBytes int64) (*Gateway, error) {
	q, err := queue.New(capBytes)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		reg:    quota.NewRegistry(),
		q:      q,
		maxNow: -1,
		stats:  make(map[string]*TenantStats),
	}, nil
}

// AddTenant registers a tenant. It never touches clock state.
func (g *Gateway) AddTenant(name string, rate, burst int64, maxPrio int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, err := g.reg.Add(name, rate, burst, maxPrio)
	if err != nil {
		return err
	}
	g.stats[t.Name] = &TenantStats{}
	return nil
}

// checkClock validates the now range (invalid parameter) and monotonicity
// against the largest accepted clock value (clock error).
func (g *Gateway) checkClock(now int64) error {
	if now < 0 || now > MaxNow {
		return ErrInvalidParam
	}
	if now < g.maxNow {
		return ErrClockBackward
	}
	return nil
}

// Ingest admits one record. Rejections are checked in the order: invalid
// parameter, clock backward, unknown tenant, priority not allowed, quota
// exceeded, queue full; a rejected call changes no state at all.
func (g *Gateway) Ingest(now int64, tenant string, prio int, size int64) (IngestResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var res IngestResult
	if prio < 0 || prio >= queue.NumPrio || size < 1 || size > g.q.Cap() {
		return res, ErrInvalidParam
	}
	if err := g.checkClock(now); err != nil {
		return res, err
	}
	t := g.reg.Get(tenant)
	if t == nil {
		return res, ErrUnknownTenant
	}
	if prio < t.MaxPrio {
		return res, ErrPrioNotAllowed
	}
	if !t.CanSpend(now, size*1000) {
		return res, ErrQuotaExceeded
	}
	if need := size - g.q.Free(); need > 0 {
		if g.q.EvictableBytes(prio) < need {
			return res, ErrQueueFull
		}
		res.Evicted = g.q.Evict(prio, need)
		for _, r := range res.Evicted {
			g.reg.Get(r.Tenant).Refund(now, r.Size*1000)
			st := g.stats[r.Tenant]
			st.EvictedBytes[r.Prio] += r.Size
			st.QueuedBytes[r.Prio] -= r.Size
		}
	}
	t.Spend(now, size*1000)
	res.Seq = g.q.Enqueue(tenant, prio, size).Seq
	st := g.stats[tenant]
	st.AcceptedBytes[prio] += size
	st.QueuedBytes[prio] += size
	g.maxNow = now
	return res, nil
}

// Drain dequeues by ascending priority then ascending seq while the next
// record fits budget, stopping at the first that does not. Token buckets
// are untouched.
func (g *Gateway) Drain(now, budget int64) ([]queue.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if budget < 0 || budget > MaxBudget {
		return nil, ErrInvalidParam
	}
	if err := g.checkClock(now); err != nil {
		return nil, err
	}
	recs := g.q.Drain(budget)
	for _, r := range recs {
		st := g.stats[r.Tenant]
		st.DrainedBytes[r.Prio] += r.Size
		st.QueuedBytes[r.Prio] -= r.Size
	}
	g.maxNow = now
	return recs, nil
}

// Stats returns a snapshot keyed by tenant name.
func (g *Gateway) Stats() map[string]TenantStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]TenantStats, len(g.stats))
	for name, st := range g.stats {
		out[name] = *st
	}
	return out
}

// Used reports the queued bytes.
func (g *Gateway) Used() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.q.Used()
}

// TenantState reports the persisted token balance and last refill time.
func (g *Gateway) TenantState(name string) (tokens, lastRefill int64, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.reg.Get(name)
	if t == nil {
		return 0, 0, false
	}
	return t.Tokens(), t.LastRefill(), true
}

// BalanceAt reports the virtual balance at now without mutating state.
func (g *Gateway) BalanceAt(now int64, name string) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.reg.Get(name)
	if t == nil {
		return 0, false
	}
	return t.Balance(now), true
}
