package cfs

import (
	"fmt"
	"math/rand"
	"testing"
)

type testOp struct {
	kind     string
	now      int64
	cpu      int
	duration int64
}

type referenceCPU struct {
	state CpuState
	local int64
	since int64
}

type referenceModel struct {
	quota         int64
	period        int64
	slice         int64
	cap           int64
	pool          int64
	lastNow       int64
	periods       int64
	nThrottled    int64
	throttledTime int64
	cpus          []referenceCPU
	queue         []int
}

type referenceSnapshot struct {
	pool          int64
	lastNow       int64
	periods       int64
	nThrottled    int64
	throttledTime int64
	cpus          []referenceCPU
	queue         []int
}

func TestRandomAgainstNaiveSimulation(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		quota := int64(rng.Intn(12) + 1)
		period := int64(rng.Intn(7) + 2)
		slice := int64(rng.Intn(8) + 1)
		burst := int64(rng.Intn(6))
		cpuCount := int64(rng.Intn(4) + 1)

		controller, err := New(quota, period, slice, burst, cpuCount)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		reference := newReferenceModel(quota, period, slice, burst, int(cpuCount))

		t.Logf("seed=%d config={Q:%d P:%d S:%d B:%d C:%d} basis=accepted replay with every boundary processed one by one",
			seed, quota, period, slice, burst, cpuCount)

		ops := 70
		if cpuCount == 1 {
			ops = 100
		}
		for i := 0; i < ops; i++ {
			op := randomOp(rng, reference, i)
			beforeRef := reference.snapshot()
			wantErr := reference.apply(op)
			gotErr := controller.applyTestOp(op)

			label := "accepted; boundaries and operation are committed"
			if wantErr != nil {
				label = "rejected; naive copy/restore leaves boundary effects uncommitted"
				reference.restore(beforeRef)
			}
			t.Logf("seed=%d op=%d input=%s output={wantErr:%v gotErr:%v} basis=%s",
				seed, i, op.String(), wantErr, gotErr, label)

			if !sameError(wantErr, gotErr) {
				t.Fatalf("seed=%d op=%d %s: want error %v, got %v", seed, i, op.String(), wantErr, gotErr)
			}
			if wantErr == nil {
				reference.lastNow = op.now
			}
			controller.lastNow = reference.lastNow
			assertReferenceSnapshot(t, seed, i, op, controller, reference)
		}
	}
}

func randomOp(rng *rand.Rand, m *referenceModel, index int) testOp {
	cpuCount := len(m.cpus)
	cpu := rng.Intn(cpuCount)
	if index%17 == 0 {
		cpu = cpuCount
	}

	now := m.lastNow
	switch rng.Intn(5) {
	case 0:
		now += int64(rng.Intn(int(3 * m.period)))
	case 1:
		now += int64(rng.Intn(int(m.period)))
	case 2:
		now = m.lastNow
	}
	if index%23 == 0 {
		now = -1
	}
	if index%29 == 0 {
		now = m.lastNow - 1
	}

	kind := []string{"wake", "run", "idle"}[rng.Intn(3)]
	duration := int64(rng.Intn(12) + 1)
	if index%19 == 0 {
		duration = 0
	}
	return testOp{kind: kind, now: now, cpu: cpu, duration: duration}
}

func newReferenceModel(quota, period, slice, burst int64, cpuCount int) *referenceModel {
	return &referenceModel{
		quota:  quota,
		period: period,
		slice:  slice,
		cap:    quota + burst,
		pool:   quota,
		cpus:   make([]referenceCPU, cpuCount),
	}
}

func (m *referenceModel) apply(op testOp) error {
	if op.cpu < 0 || op.cpu >= len(m.cpus) {
		return ErrInvalidCPU
	}
	if op.now < 0 || op.now > maxTime || (op.kind == "run" && (op.duration < 1 || op.duration > maxRun)) {
		return ErrInvalidArg
	}
	if op.now < m.lastNow {
		return ErrTimeRewind
	}

	saved := m.snapshot()
	m.applyBoundariesNaive(op.now)

	var err error
	switch op.kind {
	case "wake":
		if m.cpus[op.cpu].state != Idle {
			err = ErrNotIdle
		} else {
			m.cpus[op.cpu].state = Running
		}
	case "run":
		switch m.cpus[op.cpu].state {
		case Idle:
			err = ErrNotRunning
		case Throttled:
			err = ErrThrottled
		default:
			runtime := &m.cpus[op.cpu]
			runtime.local -= op.duration
			if runtime.local <= 0 {
				want := m.slice - runtime.local
				take := minInt64(want, m.pool)
				runtime.local += take
				m.pool -= take
				if runtime.local <= 0 {
					runtime.state = Throttled
					runtime.since = op.now
					m.queue = append(m.queue, op.cpu)
					m.nThrottled++
				}
			}
		}
	case "idle":
		switch m.cpus[op.cpu].state {
		case Idle:
			err = ErrNotRunning
		case Throttled:
			err = ErrThrottled
		default:
			runtime := &m.cpus[op.cpu]
			if runtime.local > 1 {
				slack := runtime.local - 1
				runtime.local = 1
				m.pool = minInt64(m.cap, m.pool+slack)
			}
			runtime.state = Idle
		}
	}

	if err != nil {
		m.restore(saved)
	}
	return err
}

func (m *referenceModel) applyBoundariesNaive(now int64) {
	processed := m.lastNow / m.period
	lastBoundary := now / m.period
	for processed < lastBoundary {
		processed++
		boundary := processed * m.period
		m.periods++
		m.pool = minInt64(m.cap, m.pool+m.quota)

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
	}
}

func (m *referenceModel) snapshot() referenceSnapshot {
	return referenceSnapshot{
		pool:          m.pool,
		lastNow:       m.lastNow,
		periods:       m.periods,
		nThrottled:    m.nThrottled,
		throttledTime: m.throttledTime,
		cpus:          append([]referenceCPU(nil), m.cpus...),
		queue:         append([]int(nil), m.queue...),
	}
}

func (m *referenceModel) restore(snapshot referenceSnapshot) {
	m.pool = snapshot.pool
	m.lastNow = snapshot.lastNow
	m.periods = snapshot.periods
	m.nThrottled = snapshot.nThrottled
	m.throttledTime = snapshot.throttledTime
	m.cpus = append([]referenceCPU(nil), snapshot.cpus...)
	m.queue = append([]int(nil), snapshot.queue...)
}

func (c *Controller) applyTestOp(op testOp) error {
	switch op.kind {
	case "wake":
		return c.Wake(op.now, op.cpu)
	case "run":
		return c.Run(op.now, op.cpu, op.duration)
	case "idle":
		return c.Idle(op.now, op.cpu)
	default:
		panic("unknown operation")
	}
}

func assertReferenceSnapshot(t *testing.T, seed int64, index int, op testOp, c *Controller, m *referenceModel) {
	t.Helper()
	if c.pool != m.pool {
		t.Fatalf("seed=%d op=%d %s: pool=%d want=%d", seed, index, op.String(), c.pool, m.pool)
	}
	if c.periods != m.periods || c.nThrottled != m.nThrottled || c.throttledTime != m.throttledTime {
		t.Fatalf("seed=%d op=%d %s: stats=(%d,%d,%d) want=(%d,%d,%d)",
			seed, index, op.String(), c.periods, c.nThrottled, c.throttledTime,
			m.periods, m.nThrottled, m.throttledTime)
	}
	for cpu := range m.cpus {
		if c.cpus[cpu].state != m.cpus[cpu].state ||
			c.cpus[cpu].local != m.cpus[cpu].local ||
			(c.cpus[cpu].state == Throttled && c.cpus[cpu].since != m.cpus[cpu].since) {
			t.Fatalf("seed=%d op=%d %s: cpu=%d actual=%+v want=%+v",
				seed, index, op.String(), cpu, c.cpus[cpu], m.cpus[cpu])
		}
	}
	if len(c.queue) != len(m.queue) {
		t.Fatalf("seed=%d op=%d %s: queue=%v want=%v", seed, index, op.String(), c.queue, m.queue)
	}
	for i := range m.queue {
		if c.queue[i] != m.queue[i] {
			t.Fatalf("seed=%d op=%d %s: queue=%v want=%v", seed, index, op.String(), c.queue, m.queue)
		}
	}
}

func sameError(got, want error) bool {
	return got == want
}

func (op testOp) String() string {
	return fmt.Sprintf("{kind:%s now:%d cpu:%d d:%d}", op.kind, op.now, op.cpu, op.duration)
}
