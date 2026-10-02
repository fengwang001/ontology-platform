// Package jit implements a tiered JIT hotness manager with load feedback,
// a serial compilation queue, counter decay and deoptimization penalties.
package jit

import (
	"errors"
	"math/big"
	"sync"
)

const (
	maxN        = 100000
	maxNBack    = 1000000
	maxNow      = 1000000000000000
	maxParam    = 1000000000
	maxKd       = 100
	maxDecayBit = 62
)

var (
	ErrInvalidArgument = errors.New("jit: invalid argument")
	ErrClockRegression = errors.New("jit: clock regression")
	ErrNotTier2        = errors.New("jit: method is not at tier 2 after install")
)

// Config holds the constructor parameters of a Manager.
type Config struct {
	N  int // number of methods, 1..1e5, ids 0..N-1
	A1 int64
	M1 int64
	B1 int64
	A2 int64
	M2 int64
	B2 int64
	F  int64 // load feedback divisor, 1..1e9
	Qc int   // queue capacity, 1..1e5
	D1 int64 // tier-1 compile duration
	D2 int64 // tier-2 compile duration
	Pd int64 // decay period
	C  int64 // deopt cooldown base
	Kd int64 // tier-2 ban threshold, 1..100
}

// job is one queued compilation task.
type job struct {
	method int
	target int
	start  int64
	finish int64
}

// methodState is the per-method hotness state.
type methodState struct {
	tier     int
	i        int64
	b        int64
	dc       int64
	epoch    int64
	cu       int64
	inflight bool
}

// State is the read-only view returned by Manager.State.
type State struct {
	Tier     int
	I        int64
	B        int64
	Dc       int64
	Cu       int64
	QueueLen int
	LF       int64
}

// Manager is safe for concurrent use; all operations are linearizable.
type Manager struct {
	mu      sync.Mutex
	cfg     Config
	ms      []methodState
	tNow    int64
	queue   []job
	lf      int64
	dropped int64

	headChecks int64 // unexported counter: queue-head inspections
	installs   int64 // unexported counter: installed jobs
}

// NewManager validates cfg and returns a ready Manager.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.N < 1 || cfg.N > maxN {
		return nil, ErrInvalidArgument
	}
	for _, v := range []int64{cfg.A1, cfg.M1, cfg.B1, cfg.A2, cfg.M2, cfg.B2, cfg.F, cfg.D1, cfg.D2, cfg.Pd, cfg.C} {
		if v < 1 || v > maxParam {
			return nil, ErrInvalidArgument
		}
	}
	if cfg.M1 > cfg.A1 || cfg.M2 > cfg.A2 {
		return nil, ErrInvalidArgument
	}
	if cfg.Qc < 1 || cfg.Qc > maxN {
		return nil, ErrInvalidArgument
	}
	if cfg.Kd < 1 || cfg.Kd > maxKd {
		return nil, ErrInvalidArgument
	}
	return &Manager{cfg: cfg, ms: make([]methodState, cfg.N)}, nil
}

// Call records one invocation of method m carrying n back-edges at time now.
// It returns the tier the call executed in.
func (m *Manager) Call(method int, n int64, now int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if method < 0 || method >= m.cfg.N || n < 0 || n > maxNBack || now < 0 || now > maxNow {
		return 0, ErrInvalidArgument
	}
	if now < m.tNow {
		return 0, ErrClockRegression
	}

	m.installLocked(now)
	ms := &m.ms[method]
	m.decayLocked(ms, now)

	tier := ms.tier
	ms.i++
	ms.b += n

	if ms.tier < 2 && !ms.inflight && now >= ms.cu {
		q := int64(len(m.queue))
		s := 1 + q/m.cfg.F
		d := 1 + ms.dc
		target := 0
		h2 := ms.dc < m.cfg.Kd && h2Locked(ms, d, s, &m.cfg)
		switch ms.tier {
		case 0:
			if h2 {
				target = 2
			} else if h1Locked(ms, s, &m.cfg) {
				target = 1
			}
		case 1:
			if h2 {
				target = 2
			}
		}
		if target != 0 {
			if q >= int64(m.cfg.Qc) {
				m.dropped++
			} else {
				start := now
				if m.lf > start {
					start = m.lf
				}
				dur := m.cfg.D1
				if target == 2 {
					dur = m.cfg.D2
				}
				finish := start + dur
				m.queue = append(m.queue, job{method: method, target: target, start: start, finish: finish})
				m.lf = finish
				ms.inflight = true
			}
		}
	}

	m.tNow = now
	return tier, nil
}

// Deopt deoptimizes method m (must be at tier 2 after installs) at time now.
func (m *Manager) Deopt(method int, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if method < 0 || method >= m.cfg.N || now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	if now < m.tNow {
		return ErrClockRegression
	}

	// A rejected Deopt must not change any state, so first check the tier
	// the method would have after installs, without mutating anything.
	if m.tierAfterInstallLocked(method, now) != 2 {
		return ErrNotTier2
	}

	m.installLocked(now)
	ms := &m.ms[method]
	m.decayLocked(ms, now)

	ms.tier = 0
	ms.i = 0
	ms.b = 0
	ms.dc++
	ms.cu = now + m.cfg.C*ms.dc

	m.tNow = now
	return nil
}

// State returns the view of method m at time now without mutating anything.
func (m *Manager) State(method int, now int64) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if method < 0 || method >= m.cfg.N || now < 0 || now > maxNow {
		return State{}, ErrInvalidArgument
	}
	if now < m.tNow {
		return State{}, ErrClockRegression
	}

	ms := m.ms[method]
	ms.tier = m.tierAfterInstallLocked(method, now)
	decayView(&ms, now, m.cfg.Pd)
	qlen := 0
	for _, j := range m.queue {
		if j.finish > now {
			qlen++
		}
	}
	return State{
		Tier:     ms.tier,
		I:        ms.i,
		B:        ms.b,
		Dc:       ms.dc,
		Cu:       ms.cu,
		QueueLen: qlen,
		LF:       m.lf,
	}, nil
}

// Dropped returns the number of enqueue attempts rejected due to a full queue.
func (m *Manager) Dropped() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped
}

// installLocked pops every queued job with finish <= now, in enqueue order,
// applying its target tier to the method and clearing the inflight flag.
// Each call inspects the queue head at most installs+1 times.
func (m *Manager) installLocked(now int64) {
	for len(m.queue) > 0 {
		m.headChecks++
		head := m.queue[0]
		if head.finish > now {
			break
		}
		m.queue = m.queue[1:]
		m.ms[head.method].tier = head.target
		m.ms[head.method].inflight = false
		m.installs++
	}
}

// tierAfterInstallLocked reports the tier method would have if all jobs with
// finish <= now were installed. At most one job per method can be in flight.
func (m *Manager) tierAfterInstallLocked(method int, now int64) int {
	tier := m.ms[method].tier
	for _, j := range m.queue {
		if j.finish > now {
			break
		}
		if j.method == method {
			tier = j.target
		}
	}
	return tier
}

// decayLocked applies the epoch-based decay of counters for one method.
func (m *Manager) decayLocked(ms *methodState, now int64) {
	decayView(ms, now, m.cfg.Pd)
}

// decayView shifts i and b right by min(g,62) where g is the number of decay
// epochs elapsed since the method's applied epoch, then records the epoch.
func decayView(ms *methodState, now, pd int64) {
	epoch := now / pd
	g := epoch - ms.epoch
	if g > maxDecayBit {
		g = maxDecayBit
	}
	if g > 0 {
		ms.i >>= uint(g)
		ms.b >>= uint(g)
	}
	ms.epoch = epoch
}

// geq reports x >= a*b*c exactly, using arbitrary precision for the product.
func geq(x, a, b, c int64) bool {
	prod := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	prod.Mul(prod, big.NewInt(c))
	return big.NewInt(x).Cmp(prod) >= 0
}

// h1Locked reports the tier-1 hotness condition with load scaling s.
func h1Locked(ms *methodState, s int64, cfg *Config) bool {
	if geq(ms.i, cfg.A1, s, 1) {
		return true
	}
	return geq(ms.i, cfg.M1, s, 1) && geq(ms.i+ms.b, cfg.B1, s, 1)
}

// h2Locked reports the tier-2 hotness condition with penalty d and scaling s.
// The caller must already have excluded methods banned from tier 2.
func h2Locked(ms *methodState, d, s int64, cfg *Config) bool {
	if geq(ms.i, cfg.A2, d, s) {
		return true
	}
	return geq(ms.i, cfg.M2, d, s) && geq(ms.i+ms.b, cfg.B2, d, s)
}
