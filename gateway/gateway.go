// Package gateway coordinates tenant token buckets, the bounded priority
// queue, eviction refunds and draining.
package gateway

import (
	"errors"
	"sync"

	"ontology/queue"
	"ontology/quota"
)

const (
	maxTenants   = 1000
	maxNameBytes = 64
	maxPriority  = queue.Priorities - 1
	milliPerByte = 1000
	maxNow       = int64(1_000_000_000_000)
	maxBudget    = int64(1_000_000_000_000)
	maxRate      = int64(1_000_000_000)
	maxBurst     = int64(1_000_000_000)
)

var (
	ErrInvalid       = errors.New("gateway: invalid argument")
	ErrClockBackward = errors.New("gateway: clock moved backwards")
	ErrNoTenant      = errors.New("gateway: tenant not found")
	ErrForbidden     = errors.New("gateway: priority above tenant maximum")
	ErrQuota         = errors.New("gateway: quota insufficient")
	ErrQueueFull     = queue.ErrQueueFull
	ErrDuplicate     = errors.New("gateway: duplicate tenant")
	ErrTenantLimit   = errors.New("gateway: tenant limit reached")
)

type TenantStats struct {
	AcceptedBytes [queue.Priorities]int64
	EvictedBytes  [queue.Priorities]int64
	DrainedBytes  [queue.Priorities]int64
	QueuedBytes   [queue.Priorities]int64
}

type tenant struct {
	name    string
	bucket  *quota.Bucket
	maxPrio int
	stats   TenantStats
}

type Gateway struct {
	mu          sync.Mutex
	cap, maxNow int64
	tenants     map[string]*tenant
	q           *queue.Queue
}

func New(cap int64) (*Gateway, error) {
	if cap < 1 || cap > maxBurst {
		return nil, ErrInvalid
	}
	return &Gateway{cap: cap, tenants: map[string]*tenant{}, q: queue.New(cap)}, nil
}

func (g *Gateway) AddTenant(name string, rate, burst int64, highestPrio int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(name) == 0 || len(name) > maxNameBytes || rate < 0 || rate > maxRate ||
		burst < 1 || burst > maxBurst ||
		highestPrio < 0 || highestPrio > maxPriority {
		return ErrInvalid
	}
	if _, ok := g.tenants[name]; ok {
		return ErrDuplicate
	}
	if len(g.tenants) >= maxTenants {
		return ErrTenantLimit
	}
	g.tenants[name] = &tenant{name: name, bucket: quota.NewBucket(rate, burst), maxPrio: highestPrio}
	return nil
}

type IngressResult struct {
	Seq     int64
	Evicted []queue.Record
}

func (g *Gateway) Ingest(now int64, tenantName string, prio int, size int64) (IngressResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < 0 || now > maxNow || prio < 0 || prio > maxPriority || size < 1 || size > g.cap || tenantName == "" {
		return IngressResult{}, ErrInvalid
	}
	if now < g.maxNow {
		return IngressResult{}, ErrClockBackward
	}
	t, ok := g.tenants[tenantName]
	if !ok {
		return IngressResult{}, ErrNoTenant
	}
	if prio < t.maxPrio {
		return IngressResult{}, ErrForbidden
	}
	cost := size * milliPerByte
	if t.bucket.PeekBalance(now) < cost {
		return IngressResult{}, ErrQuota
	}
	plan, err := g.q.PlanAdmit(prio, size)
	if err != nil {
		return IngressResult{}, ErrQueueFull
	}
	evicted := make([]queue.Record, 0, len(plan.Evict))
	for _, r := range plan.Evict {
		ev := g.tenants[r.Tenant]
		ev.bucket.Refund(now, r.Size*milliPerByte)
		ev.stats.EvictedBytes[r.Prio] += r.Size
		ev.stats.QueuedBytes[r.Prio] -= r.Size
		evicted = append(evicted, *r)
	}
	t.bucket.Debit(now, cost)
	rec := g.q.Commit(tenantName, prio, size, plan)
	t.stats.AcceptedBytes[prio] += size
	t.stats.QueuedBytes[prio] += size
	g.maxNow = now
	return IngressResult{Seq: rec.Seq, Evicted: evicted}, nil
}

func (g *Gateway) Drain(now, budget int64) ([]queue.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < 0 || now > maxNow || budget < 0 || budget > maxBudget {
		return nil, ErrInvalid
	}
	if now < g.maxNow {
		return nil, ErrClockBackward
	}
	recs := g.q.PopDrain(budget)
	out := make([]queue.Record, len(recs))
	for i, r := range recs {
		tn := g.tenants[r.Tenant]
		tn.stats.DrainedBytes[r.Prio] += r.Size
		tn.stats.QueuedBytes[r.Prio] -= r.Size
		out[i] = *r
	}
	g.maxNow = now
	return out, nil
}

func (g *Gateway) Stats() map[string]TenantStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]TenantStats, len(g.tenants))
	for name, t := range g.tenants {
		out[name] = t.stats
	}
	return out
}

func (g *Gateway) Snapshot() []queue.Record {
	g.mu.Lock()
	defer g.mu.Unlock()
	recs := g.q.Snapshot()
	out := make([]queue.Record, len(recs))
	for i := range recs {
		out[i] = *recs[i]
	}
	return out
}

func (g *Gateway) tokenInfo(name string, last bool) (int64, bool) {
	t, ok := g.tenants[name]
	if !ok {
		return 0, false
	}
	if last {
		return t.bucket.LastRefill(), true
	}
	return t.bucket.Tokens(), true
}

func (g *Gateway) TokenBalance(name string) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokenInfo(name, false)
}

func (g *Gateway) LastRefill(name string) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokenInfo(name, true)
}

func (g *Gateway) Used() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.q.Used()
}

func (g *Gateway) Cap() int64 {
	return g.cap
}
