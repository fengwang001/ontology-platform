package cfs

import (
	"errors"
	"sync"
)

type CpuState int

const (
	Idle CpuState = iota
	Running
	Throttled
)

var (
	ErrInvalidConfig = errors.New("invalid CFS configuration")
	ErrInvalidCPU    = errors.New("cpu out of range")
	ErrInvalidArg    = errors.New("invalid argument")
	ErrTimeRewind    = errors.New("time rewind")
	ErrNotIdle       = errors.New("cpu is not idle")
	ErrNotRunning    = errors.New("cpu is not running")
	ErrThrottled     = errors.New("cpu is throttled")
)

type CpuStatus struct {
	State CpuState
	Local int64
	Since int64
}

type StatsSnapshot struct {
	Periods       int64
	NThrottled    int64
	ThrottledTime int64
}

type cpuRuntime struct {
	state CpuState
	local int64
	since int64
}

type model struct {
	quota         int64
	period        int64
	slice         int64
	cap           int64
	pool          int64
	lastNow       int64
	periods       int64
	nThrottled    int64
	throttledTime int64
	cpus          []cpuRuntime
	queue         []int
	boundaryIters int64
}

type Controller struct {
	mu       sync.RWMutex
	cpuCount int64
	model
}

const (
	maxTime     int64 = 1_000_000_000_000_000
	maxRun      int64 = 1_000_000
	maxConfig   int64 = 1_000_000_000
	maxCPUCount int64 = 64
)

func New(quota, period, slice, burst, cpus int64) (*Controller, error) {
	if quota < 1 || quota > maxConfig ||
		period < 1 || period > maxConfig ||
		slice < 1 || slice > maxConfig ||
		burst < 0 || burst > maxConfig ||
		cpus < 1 || cpus > maxCPUCount {
		return nil, ErrInvalidConfig
	}

	m := model{
		quota:  quota,
		period: period,
		slice:  slice,
		cap:    quota + burst,
		pool:   quota,
		cpus:   make([]cpuRuntime, cpus),
	}
	return &Controller{cpuCount: cpus, model: m}, nil
}

func (c *Controller) Wake(now int64, cpu int) error {
	if err := c.checkCPU(cpu); err != nil {
		return err
	}
	if now < 0 || now > maxTime {
		return ErrInvalidArg
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrTimeRewind
	}
	next := c.copyModel()
	next.applyBoundaries(now)
	if next.cpus[cpu].state != Idle {
		return ErrNotIdle
	}
	next.cpus[cpu].state = Running
	next.lastNow = now
	c.model = next
	return nil
}

func (c *Controller) Run(now int64, cpu int, duration int64) error {
	if err := c.checkCPU(cpu); err != nil {
		return err
	}
	if now < 0 || now > maxTime || duration < 1 || duration > maxRun {
		return ErrInvalidArg
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrTimeRewind
	}
	next := c.copyModel()
	next.applyBoundaries(now)
	switch next.cpus[cpu].state {
	case Idle:
		return ErrNotRunning
	case Throttled:
		return ErrThrottled
	}

	runtime := &next.cpus[cpu]
	runtime.local -= duration
	if runtime.local <= 0 {
		want := next.slice - runtime.local
		take := minInt64(want, next.pool)
		runtime.local += take
		next.pool -= take
		if runtime.local <= 0 {
			runtime.state = Throttled
			runtime.since = now
			next.queue = append(next.queue, cpu)
			next.nThrottled++
		}
	}
	next.lastNow = now
	c.model = next
	return nil
}

func (c *Controller) Idle(now int64, cpu int) error {
	if err := c.checkCPU(cpu); err != nil {
		return err
	}
	if now < 0 || now > maxTime {
		return ErrInvalidArg
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.lastNow {
		return ErrTimeRewind
	}
	next := c.copyModel()
	next.applyBoundaries(now)
	switch next.cpus[cpu].state {
	case Idle:
		return ErrNotRunning
	case Throttled:
		return ErrThrottled
	}

	runtime := &next.cpus[cpu]
	if runtime.local > 1 {
		slack := runtime.local - 1
		runtime.local = 1
		next.pool = minInt64(next.cap, next.pool+slack)
	}
	runtime.state = Idle
	next.lastNow = now
	c.model = next
	return nil
}

func (c *Controller) Stats() StatsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return StatsSnapshot{
		Periods:       c.periods,
		NThrottled:    c.nThrottled,
		ThrottledTime: c.throttledTime,
	}
}

func (c *Controller) State(cpu int) (CpuStatus, error) {
	if err := c.checkCPU(cpu); err != nil {
		return CpuStatus{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	runtime := c.cpus[cpu]
	return CpuStatus{
		State: runtime.state,
		Local: runtime.local,
		Since: runtime.since,
	}, nil
}

func (c *Controller) Pool() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pool
}

func (c *Controller) Queue() []int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]int(nil), c.queue...)
}

func (c *Controller) checkCPU(cpu int) error {
	if cpu < 0 || int64(cpu) >= c.cpuCount {
		return ErrInvalidCPU
	}
	return nil
}

func (c *Controller) copyModel() model {
	next := c.model
	next.cpus = append([]cpuRuntime(nil), c.cpus...)
	next.queue = append([]int(nil), c.queue...)
	return next
}

func (m *model) applyBoundaries(now int64) {
	processed := m.lastNow / m.period
	remaining := now/m.period - processed

	for remaining > 0 {
		if len(m.queue) == 0 {
			m.boundaryIters++
			m.periods += remaining
			m.pool = addRepeatedQuota(m.pool, remaining, m.quota, m.cap)
			processed += remaining
			remaining = 0
			continue
		}

		m.boundaryIters++
		processed++
		boundary := processed * m.period
		m.periods++
		m.pool = minInt64(m.cap, m.pool+m.quota)
		remaining--

		for len(m.queue) > 0 {
			cpu := m.queue[0]
			runtime := &m.cpus[cpu]
			need := 1 - runtime.local
			take := minInt64(need, m.pool)
			runtime.local += take
			m.pool -= take
			if runtime.local > 0 {
				runtime.state = Running
				m.throttledTime += boundary - runtime.since
				m.queue = m.queue[1:]
			}
			if take < need {
				break
			}
		}

		if remaining > 0 && len(m.queue) > 0 {
			cpu := m.queue[0]
			runtime := &m.cpus[cpu]
			need := 1 - runtime.local
			required := (need + m.quota - 1) / m.quota
			m.boundaryIters++
			if required > remaining {
				runtime.local += remaining * m.quota
				m.periods += remaining
				processed += remaining
				m.pool = 0
				remaining = 0
				continue
			}

			skipped := required - 1
			runtime.local += skipped * m.quota
			m.periods += skipped
			processed += skipped
			remaining -= skipped
		}
	}
}

func addRepeatedQuota(value, periods, quota, cap int64) int64 {
	room := cap - value
	if periods > room/quota {
		return cap
	}
	return value + periods*quota
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
