package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig        = errors.New("invalid rolling update planner config")
	ErrInvalidArgument      = errors.New("invalid operation argument")
	ErrClockRegression      = errors.New("clock regression")
	ErrNewReadyOutOfRange   = errors.New("new ready count exceeds unready new replicas")
	ErrNewFailOutOfRange    = errors.New("new fail count exceeds ready new replicas")
	ErrOldUnreadyOutOfRange = errors.New("old unready count exceeds ready old replicas")
)

type Config struct {
	DesiredReplicas       int
	MaxSurgePercent       int
	MaxUnavailablePercent int
	MinReadyMillis        int64
	StallSteps            int
}

type StepResult struct {
	Created int
	Cleaned int
	Reduced int
	Stalled bool
}

type PlannerSnapshot struct {
	OldReady      int
	OldUnready    int
	NewUnready    int
	NewReady      int
	StableReady   int
	Available     int
	StallCount    int
	AcceptedNow   int64
	HasAcceptedOp bool
	ReadyBatches  []ReadyBatch
}

type ReadyBatch struct {
	ReadyAt int64
	Count   int
}

type RollingUpdatePlanner struct {
	mu sync.Mutex

	desiredReplicas      int
	maxSurge             int
	maxUnavailable       int
	minReadyMillis       int64
	stallSteps           int
	oldReady             int
	oldUnready           int
	newUnready           int
	newReady             int
	stallCount           int
	acceptedNow          int64
	hasAcceptedOperation bool
	readyBatches         []ReadyBatch
}

func NewRollingUpdatePlanner(config Config) (*RollingUpdatePlanner, error) {
	if config.DesiredReplicas < 1 || config.DesiredReplicas > 1_000_000 ||
		config.MaxSurgePercent < 0 || config.MaxSurgePercent > 100 ||
		config.MaxUnavailablePercent < 0 || config.MaxUnavailablePercent > 100 ||
		config.MinReadyMillis < 0 || config.MinReadyMillis > 1_000_000_000_000 ||
		config.StallSteps < 1 || config.StallSteps > 1000 {
		return nil, ErrInvalidConfig
	}

	maxSurge := ceilDiv(config.DesiredReplicas*config.MaxSurgePercent, 100)
	maxUnavailable := config.DesiredReplicas * config.MaxUnavailablePercent / 100
	if maxSurge == 0 && maxUnavailable == 0 {
		maxUnavailable = 1
	}

	return &RollingUpdatePlanner{
		desiredReplicas: config.DesiredReplicas,
		maxSurge:        maxSurge,
		maxUnavailable:  maxUnavailable,
		minReadyMillis:  config.MinReadyMillis,
		stallSteps:      config.StallSteps,
		oldReady:        config.DesiredReplicas,
	}, nil
}

func (p *RollingUpdatePlanner) Step(now int64) (StepResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.validateTime(now); err != nil {
		return StepResult{}, err
	}

	totalBefore := p.oldReady + p.oldUnready + p.newReady + p.newUnready
	availableBefore := p.oldReady + p.stableReadyLocked(now)

	created := maxInt(0, minInt(p.desiredReplicas+p.maxSurge-totalBefore, p.desiredReplicas-(p.newReady+p.newUnready)))
	cleaned := p.oldUnready
	reduced := minInt(p.oldReady, maxInt(0, availableBefore-(p.desiredReplicas-p.maxUnavailable)))

	p.newUnready += created
	p.oldUnready -= cleaned
	p.oldReady -= reduced
	p.accept(now)

	if p.doneLocked(now) {
		p.stallCount = 0
	} else if created+cleaned+reduced > 0 {
		p.stallCount = 0
	} else {
		p.stallCount++
	}

	return StepResult{
		Created: created,
		Cleaned: cleaned,
		Reduced: reduced,
		Stalled: p.stallCount >= p.stallSteps,
	}, nil
}

func (p *RollingUpdatePlanner) NewReady(count int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.validateCountAndTime(count, now); err != nil {
		return err
	}
	if count > p.newUnready {
		return ErrNewReadyOutOfRange
	}

	p.newUnready -= count
	p.newReady += count
	if count > 0 {
		p.readyBatches = append(p.readyBatches, ReadyBatch{ReadyAt: now, Count: count})
	}
	p.accept(now)
	p.stallCount = 0
	return nil
}

func (p *RollingUpdatePlanner) NewFail(count int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.validateCountAndTime(count, now); err != nil {
		return err
	}
	if count > p.newReady {
		return ErrNewFailOutOfRange
	}

	remaining := count
	for index := len(p.readyBatches) - 1; index >= 0 && remaining > 0; index-- {
		batch := &p.readyBatches[index]
		removed := minInt(remaining, batch.Count)
		batch.Count -= removed
		remaining -= removed
	}

	insertAt := 0
	for _, batch := range p.readyBatches {
		if batch.Count > 0 {
			p.readyBatches[insertAt] = batch
			insertAt++
		}
	}
	p.readyBatches = p.readyBatches[:insertAt]

	p.newReady -= count
	p.newUnready += count
	p.accept(now)
	return nil
}

func (p *RollingUpdatePlanner) OldUnready(count int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.validateCountAndTime(count, now); err != nil {
		return err
	}
	if count > p.oldReady {
		return ErrOldUnreadyOutOfRange
	}

	p.oldReady -= count
	p.oldUnready += count
	p.accept(now)
	return nil
}

func (p *RollingUpdatePlanner) Done() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doneLocked(p.acceptedNow)
}

func (p *RollingUpdatePlanner) Snapshot() PlannerSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	stableReady := p.stableReadyLocked(p.acceptedNow)
	snapshot := PlannerSnapshot{
		OldReady:      p.oldReady,
		OldUnready:    p.oldUnready,
		NewUnready:    p.newUnready,
		NewReady:      p.newReady,
		StableReady:   stableReady,
		Available:     p.oldReady + stableReady,
		StallCount:    p.stallCount,
		AcceptedNow:   p.acceptedNow,
		HasAcceptedOp: p.hasAcceptedOperation,
		ReadyBatches:  append([]ReadyBatch(nil), p.readyBatches...),
	}
	return snapshot
}

func (p *RollingUpdatePlanner) validateCountAndTime(count int, now int64) error {
	if count < 1 || now < 0 {
		return ErrInvalidArgument
	}
	return p.validateTime(now)
}

func (p *RollingUpdatePlanner) validateTime(now int64) error {
	if now < 0 {
		return ErrInvalidArgument
	}
	if p.hasAcceptedOperation && now < p.acceptedNow {
		return ErrClockRegression
	}
	return nil
}

func (p *RollingUpdatePlanner) accept(now int64) {
	p.acceptedNow = now
	p.hasAcceptedOperation = true
}

func (p *RollingUpdatePlanner) stableReadyLocked(now int64) int {
	if p.minReadyMillis == 0 {
		return p.newReady
	}

	stableReady := 0
	for _, batch := range p.readyBatches {
		if now-batch.ReadyAt >= p.minReadyMillis {
			stableReady += batch.Count
		}
	}
	return stableReady
}

func (p *RollingUpdatePlanner) doneLocked(now int64) bool {
	return p.oldReady == 0 && p.oldUnready == 0 && p.newReady == p.desiredReplicas &&
		p.stableReadyLocked(now) == p.desiredReplicas
}

func ceilDiv(value, divisor int) int {
	return (value + divisor - 1) / divisor
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
