// Package saga 实现带补偿的分布式事务（SAGA）编排器。
// 时间完全由构造时注入的 now func() int64（毫秒）提供；本包不调用任何 time 包函数。
package saga

import (
	"errors"
	"fmt"
	"sync"

	"ontology/compens"
	"ontology/journal"
	"ontology/step"
)

// Config 是编排器的资源上限配置。零值中 MaxSteps<=0 表示不限，
// MaxRetries<=0 视为 1（仅尝试一次），MaxJournalRecords<=0 表示不限。
type Config struct {
	MaxSteps          int
	MaxRetries        int
	MaxJournalRecords int
}

// Calls 记录一个实例中动作被「真实调用」的次数（重试逐次累加）。
type Calls struct {
	Forward       map[string]int
	Compensate    map[string]int
	ForwardAll    int
	CompensateAll int
}

type inst struct {
	mu      sync.Mutex
	running bool
	steps   []step.Step
	calls   Calls
	cached  State
}

// Orchestrator 串起 step/compens/journal，所有公开方法可并发调用。
type Orchestrator struct {
	now     func() int64
	cfg     Config
	journal *journal.Journal
	mu      sync.RWMutex
	insts   map[string]*inst
	// resumeReads：非导出计数器，记录一次 Resume 读取了多少条日志记录。
	resumeReads int
}

// New 构造编排器。now 为注入时钟（毫秒），必须非 nil。
func New(now func() int64, cfg Config) (*Orchestrator, error) {
	if now == nil {
		return nil, errors.New("saga: nil clock")
	}
	if cfg.MaxRetries < 0 {
		return nil, ErrMaxRetries
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 1
	}
	return &Orchestrator{
		now:     now,
		cfg:     cfg,
		journal: journal.New(cfg.MaxJournalRecords),
		insts:   map[string]*inst{},
	}, nil
}

// Run 创建一个实例并从头执行 SAGA。
func (o *Orchestrator) Run(id string, steps []step.Step) (State, error) {
	if err := o.validate(steps); err != nil {
		return State{}, err
	}
	it := &inst{
		steps: steps,
		calls: Calls{Forward: map[string]int{}, Compensate: map[string]int{}},
	}
	if !o.register(id, it) {
		return State{}, fmt.Errorf("saga: instance %q already exists", id)
	}
	return o.start(id, it)
}

// start 在实例锁内把一个已注册实例推进到终态（Run/并发测试共用）。
func (o *Orchestrator) start(id string, it *inst) (State, error) {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.running = true
	defer func() { it.running = false }()

	st, err := o.drive(id, it)
	return st, err
}

// register 原子注册一个新实例；已存在同名实例时返回 false。
func (o *Orchestrator) register(id string, it *inst) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, exists := o.insts[id]; exists {
		return false
	}
	o.insts[id] = it
	return true
}

// Resume 依据日志把实例推进到终态。幂等：已记录成功的动作不会再被调用。
func (o *Orchestrator) Resume(id string) (State, error) {
	o.mu.RLock()
	it, ok := o.insts[id]
	o.mu.RUnlock()
	if !ok {
		return State{}, ErrInstanceNotFound
	}
	if !it.mu.TryLock() {
		return State{}, ErrInstanceRunning
	}
	defer it.mu.Unlock()
	if it.running {
		return State{}, ErrInstanceRunning
	}
	it.running = true
	defer func() { it.running = false }()

	o.resumeReads = 0
	last, ok := o.journal.Last(id)
	o.resumeReads++
	if !ok {
		return State{}, ErrInstanceNotFound
	}
	// 终态快速判定：仅读末条记录即可，不随日志长度增长。
	if last.Direction == journal.Forward && last.Result == journal.Success &&
		last.StepIndex == len(it.steps)-1 {
		if it.cached.Status == 0 && it.cached.InstanceID == "" {
			it.cached = o.snapshotLocked(id, it)
		}
		return it.cached, nil
	}
	if last.Direction == journal.Compensate {
		switch it.cached.Status {
		case Compensated:
			return it.cached, nil
		case CompensateFailed:
			// 仍有未完成补偿，需要继续推进。
		default:
		}
	}
	st, err := o.drive(id, it)
	return st, err
}

func (o *Orchestrator) validate(steps []step.Step) error {
	if err := compens.Validate(steps); err != nil {
		switch {
		case errors.Is(err, compens.ErrNoSteps):
			return ErrNoSteps
		case errors.Is(err, compens.ErrDuplicateKey):
			return ErrDuplicateKey
		}
		return err
	}
	if o.cfg.MaxSteps > 0 && len(steps) > o.cfg.MaxSteps {
		return ErrMaxSteps
	}
	return nil
}

func mapJournalError(err error) error {
	if errors.Is(err, journal.ErrLimitExceeded) {
		return ErrJournalLimit
	}
	return err
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
