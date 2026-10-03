package registry

import (
	"sync"

	"ontology/acl"
	"ontology/policy"
)

type state = policy.State
type reusePolicy = policy.ReusePolicy
type conflictPolicy = policy.ConflictPolicy

const (
	stRunning    = policy.Running
	stCompleted  = policy.Completed
	stFailed     = policy.Failed
	stCancelled  = policy.Cancelled
	stTerminated = policy.Terminated
)

const (
	maxRetention = 1_000_000_000
	maxCapacity  = 1_000_000
	maxNow       = 100_000_000_000_000
)

// rec 是每个 ID 至多一条的存活记录。
type rec struct {
	run     uint64
	owner   []byte
	state   state
	endAt   int64
	version uint64
}

// Registry 是工作流实例登记表。
type Registry struct {
	mu      sync.Mutex
	r       int64
	n       int
	clock   int64
	nextRun uint64
	live    map[string]*rec
	expiry  expiryHeap
	acl     *acl.ACL
	peep    uint64
}

// New 构造登记表；R 为保留期，N 为容量。
func New(R int64, N int, a *acl.ACL) (*Registry, error) {
	if R < 1 || R > maxRetention || N < 1 || N > maxCapacity {
		return nil, ErrArgument
	}
	if a == nil {
		a = acl.New()
	}
	return &Registry{
		r:      R,
		n:      N,
		live:   make(map[string]*rec),
		expiry: make(expiryHeap, 0),
		acl:    a,
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// Start 按策略仲裁一次启动，返回新建或复用的 run 号。
func (g *Registry) Start(id, p []byte, reuse reusePolicy, conflict conflictPolicy, now int64) (uint64, error) {
	if len(id) == 0 || len(p) == 0 || !policy.ValidReuse(reuse) ||
		!policy.ValidConflict(conflict) || !validNow(now) {
		return 0, ErrArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.clock {
		return 0, ErrClock
	}
	g.purgeExpired(now)
	key := string(id)
	e, exists := g.live[key]
	var v policy.Verdict
	if !exists {
		v = policy.VerdictNew
	} else {
		v = policy.Decide(true, policy.Existing{Run: e.run, State: e.state}, reuse, conflict)
	}
	switch v {
	case policy.VerdictRejectRunning:
		return 0, ErrRunning
	case policy.VerdictUseExisting:
		// 幂等复用：不新建、不改记录、不查权限，但视为成功并推进时钟。
		g.clock = now
		return e.run, nil
	case policy.VerdictTerminateOK:
		// 调用方已明确要求取代运行中者；此路径绕过 reuse，也不检查容量。
		if !g.acl.CanTerminate(p, e.owner) {
			return 0, ErrDenied
		}
		e.state = stTerminated
		e.endAt = now
		return g.replaceLocked(key, p, now), nil
	case policy.VerdictRejectReuse:
		return 0, ErrReuse
	case policy.VerdictReplace:
		// 复用已结束记录：原位替换，ID 数不增加，不检查容量。
		return g.replaceLocked(key, p, now), nil
	default: // VerdictNew：无存活记录。
		if len(g.live) >= g.n {
			return 0, ErrCapacity
		}
		return g.createLocked(key, p, now), nil
	}
}

// createLocked 新建一条 Running 记录并消耗一个 run 号（容量已由调用方检查）。
func (g *Registry) createLocked(key string, p []byte, now int64) uint64 {
	g.nextRun++
	g.live[key] = &rec{run: g.nextRun, owner: append([]byte(nil), p...), state: stRunning, version: 1}
	g.clock = now
	return g.nextRun
}

// replaceLocked 以新 Running 记录原位替换现有记录：ID 数不增加，故不检查容量。
func (g *Registry) replaceLocked(key string, p []byte, now int64) uint64 {
	old := g.live[key]
	g.nextRun++
	g.live[key] = &rec{
		run:     g.nextRun,
		owner:   append([]byte(nil), p...),
		state:   stRunning,
		version: old.version + 1,
	}
	g.clock = now
	return g.nextRun
}

// Finish 结束指定 run。
func (g *Registry) Finish(id []byte, run uint64, st state, now int64) error {
	if len(id) == 0 || !policy.ValidFinishState(st) || !validNow(now) {
		return ErrArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.clock {
		return ErrClock
	}
	g.purgeExpired(now)
	key := string(id)
	e, ok := g.live[key]
	if !ok {
		return ErrNotFound
	}
	if e.run != run {
		return ErrStale
	}
	if e.state != stRunning {
		return ErrNotRunning
	}
	e.state = st
	e.endAt = now
	g.pushExpiry(key, e.version, now)
	g.clock = now
	return nil
}

// Count 返回 now 时刻的存活 ID 数（只读，不推进时钟）。
func (g *Registry) Count(now int64) (int, error) {
	if !validNow(now) {
		return 0, ErrArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.clock {
		return 0, ErrClock
	}
	g.purgeExpired(now)
	return len(g.live), nil
}
