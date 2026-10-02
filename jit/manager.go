package jit

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidArgument reports an out-of-range method, back-edge count, or timestamp.
	ErrInvalidArgument = errors.New("jit: invalid argument")
	// ErrClockRewound reports now earlier than the latest accepted mutation time.
	ErrClockRewound = errors.New("jit: clock rewound")
	// ErrNotTier2 reports Deopt on a method that is not tier 2 after installation.
	ErrNotTier2 = errors.New("jit: method is not at tier 2 after installation")
)

// Config defines tier thresholds, feedback scaling, compile timing, decay, and deopt rules.
type Config struct {
	Methods             int64
	Tier1Calls          int64
	Tier1MinCalls       int64
	Tier1Total          int64
	Tier2Calls          int64
	Tier2MinCalls       int64
	Tier2Total          int64
	FeedbackDivisor     int64
	QueueCapacity       int64
	Tier1CompileTime    int64
	Tier2CompileTime    int64
	DecayPeriod         int64
	DeoptCooldownBase   int64
	Tier2DeoptThreshold int64
}

// Job is a queued compilation with its serial start and finish timestamps.
type Job struct {
	Method int64
	Target int
	Start  int64
	Finish int64
}

// MethodState is the reported state of one method.
type MethodState struct {
	Tier      int
	Calls     Counter
	BackEdges Counter
	Deopts    int64
	CoolUntil int64
}

// Snapshot combines one method's hypothetical State view with queue-wide timing data.
type Snapshot struct {
	MethodState
	QueuedJobs int
	LastFinish int64
}

type method struct {
	tier      int
	calls     Counter
	backEdges Counter
	deopts    int64
	epoch     int64
	coolUntil int64
	inFlight  bool
}

// Manager is a concurrency-safe deterministic tiered-JIT heat manager.
type Manager struct {
	cfg Config

	mu         sync.Mutex
	methods    []method
	queue      []Job
	lastFinish int64
	now        int64
	discarded  int64
	headChecks int
}

// NewManager validates cfg and constructs a manager with N interpreted methods.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.Methods < 1 || cfg.Methods > 100000 ||
		cfg.Tier1Calls < 1 || cfg.Tier1MinCalls < 1 || cfg.Tier1Total < 1 ||
		cfg.Tier2Calls < 1 || cfg.Tier2MinCalls < 1 || cfg.Tier2Total < 1 ||
		cfg.Tier1MinCalls > cfg.Tier1Calls || cfg.Tier2MinCalls > cfg.Tier2Calls ||
		cfg.FeedbackDivisor < 1 ||
		cfg.QueueCapacity < 1 || cfg.QueueCapacity > 100000 ||
		cfg.Tier1CompileTime < 1 || cfg.Tier2CompileTime < 1 ||
		cfg.DecayPeriod < 1 || cfg.DeoptCooldownBase < 1 ||
		cfg.Tier2DeoptThreshold < 1 || cfg.Tier2DeoptThreshold > 100 {
		return nil, ErrInvalidArgument
	}

	return &Manager{
		cfg:     cfg,
		methods: make([]method, cfg.Methods),
	}, nil
}

// Call records one invocation with n back edges and returns the tier executing that call.
func (m *Manager) Call(method int64, backEdges int64, now int64) (int, error) {
	if err := m.validate(method, backEdges, now); err != nil {
		return 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkTimeLocked(now); err != nil {
		return 0, err
	}
	entry := &m.methods[method]
	m.installDueLocked(now)
	m.applyDecayLocked(entry, now)

	tier := entry.tier
	entry.calls = entry.calls.add(counterFromInt64(1))
	entry.backEdges = entry.backEdges.add(counterFromInt64(backEdges))
	m.promoteLocked(method, entry, now)
	m.now = now

	return tier, nil
}

// Deopt moves an installed tier-2 method back to tier 0 and applies its cooldown.
func (m *Manager) Deopt(method int64, now int64) error {
	if err := m.validate(method, 0, now); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkTimeLocked(now); err != nil {
		return err
	}
	entry := &m.methods[method]
	if m.installedTierLocked(method, now) != 2 {
		return ErrNotTier2
	}

	m.installDueLocked(now)
	m.applyDecayLocked(entry, now)
	entry.tier = 0
	entry.calls = Counter{}
	entry.backEdges = Counter{}
	entry.deopts++
	entry.coolUntil = now + m.cfg.DeoptCooldownBase*entry.deopts
	m.now = now

	return nil
}

// State reports a non-mutating install-then-decay view at now.
func (m *Manager) State(method int64, now int64) (Snapshot, error) {
	if err := m.validate(method, 0, now); err != nil {
		return Snapshot{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkTimeLocked(now); err != nil {
		return Snapshot{}, err
	}

	entry := m.methods[method]
	due := m.dueCountLocked(now)
	m.applyDecayLocked(&entry, now)
	tier := entry.tier
	for idx := 0; idx < due; idx++ {
		if m.queue[idx].Method == method {
			tier = m.queue[idx].Target
		}
	}

	return Snapshot{
		MethodState: MethodState{
			Tier:      tier,
			Calls:     entry.calls,
			BackEdges: entry.backEdges,
			Deopts:    entry.deopts,
			CoolUntil: entry.coolUntil,
		},
		QueuedJobs: len(m.queue) - due,
		LastFinish: m.lastFinish,
	}, nil
}

// Discarded returns the number of promotion attempts rejected by a full compile queue.
func (m *Manager) Discarded() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.discarded
}

func (m *Manager) validate(method int64, backEdges int64, now int64) error {
	if method < 0 || method >= m.cfg.Methods || backEdges < 0 || backEdges > 1000000 ||
		now < 0 || now > 1000000000000000 {
		return ErrInvalidArgument
	}
	return nil
}

func (m *Manager) checkTimeLocked(now int64) error {
	if now < m.now {
		return ErrClockRewound
	}
	return nil
}

func (m *Manager) dueCountAndHeadChecksLocked(now int64) (int, int) {
	headChecks := 0
	for headChecks <= len(m.queue) {
		if headChecks == len(m.queue) || m.queue[headChecks].Finish > now {
			return headChecks, headChecks + 1
		}
		headChecks++
	}
	return headChecks, headChecks
}

func (m *Manager) dueCountLocked(now int64) int {
	due, _ := m.dueCountAndHeadChecksLocked(now)
	return due
}

func (m *Manager) installDueLocked(now int64) {
	due, headChecks := m.dueCountAndHeadChecksLocked(now)
	m.headChecks = headChecks
	for len(m.queue) > 0 && due > 0 {
		job := m.queue[0]
		m.queue = m.queue[1:]
		due--
		m.methods[job.Method].tier = job.Target
		m.methods[job.Method].inFlight = false
	}
}

func (m *Manager) installedTierLocked(method int64, now int64) int {
	tier := m.methods[method].tier
	for idx := 0; idx < len(m.queue); idx++ {
		job := m.queue[idx]
		if job.Finish > now {
			break
		}
		if job.Method == method {
			tier = job.Target
		}
	}
	return tier
}

func (m *Manager) applyDecayLocked(entry *method, now int64) {
	nextEpoch := now / m.cfg.DecayPeriod
	gap := nextEpoch - entry.epoch
	if gap > 0 {
		if gap > 62 {
			gap = 62
		}
		shift := uint(gap)
		entry.calls = entry.calls.shiftRight(shift)
		entry.backEdges = entry.backEdges.shiftRight(shift)
		entry.epoch = nextEpoch
	}
}

func (m *Manager) promoteLocked(method int64, entry *method, now int64) {
	if entry.tier >= 2 || entry.inFlight || now < entry.coolUntil {
		return
	}

	queued := int64(len(m.queue))
	scale := 1 + queued/m.cfg.FeedbackDivisor
	deoptFactor := 1 + entry.deopts

	callsValue := entry.calls
	total := callsValue.add(entry.backEdges)
	h2 := false
	if entry.deopts < m.cfg.Tier2DeoptThreshold {
		h2 = callsValue.atLeast(multiply3(m.cfg.Tier2Calls, deoptFactor, scale)) ||
			(callsValue.atLeast(multiply3(m.cfg.Tier2MinCalls, deoptFactor, scale)) &&
				total.atLeast(multiply3(m.cfg.Tier2Total, deoptFactor, scale)))
	}

	h1 := callsValue.atLeast(multiply64(m.cfg.Tier1Calls, scale)) ||
		(callsValue.atLeast(multiply64(m.cfg.Tier1MinCalls, scale)) &&
			total.atLeast(multiply64(m.cfg.Tier1Total, scale)))

	target := 0
	if entry.tier == 0 && h2 {
		target = 2
	} else if entry.tier == 0 && h1 {
		target = 1
	} else if entry.tier == 1 && h2 {
		target = 2
	}

	if target == 0 {
		return
	}
	if queued >= m.cfg.QueueCapacity {
		m.discarded++
		return
	}

	start := now
	if m.lastFinish > start {
		start = m.lastFinish
	}
	duration := m.cfg.Tier1CompileTime
	if target == 2 {
		duration = m.cfg.Tier2CompileTime
	}
	finish := start + duration
	job := Job{
		Method: method,
		Target: target,
		Start:  start,
		Finish: finish,
	}
	m.queue = append(m.queue, job)
	m.lastFinish = finish
	entry.inFlight = true
}
