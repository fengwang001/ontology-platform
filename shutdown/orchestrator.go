package shutdown

import (
	"errors"
	"log/slog"
	"sync"
)

type StopMethod int

const (
	NotStopped StopMethod = iota
	Graceful
	Killed
)

type ServiceState struct {
	ID              string
	GracePeriodMS   int64
	TerminationTime int64
	StopTime        int64
	StopMethod      StopMethod
}

type Orchestrator struct {
	mu        sync.Mutex
	services  map[string]*service
	order     []string
	started   bool
	hasLastAt bool
	lastAt    int64
	kills     *killHeap
	logger    *slog.Logger
}

type service struct {
	id            string
	gracePeriodMS int64
	dependencies  map[string]struct{}
	dependents    map[string]struct{}
	terminated    bool
	terminationAt int64
	stopped       bool
	stoppedAt     int64
	stopMethod    StopMethod
}

func New(logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Orchestrator{
		services: make(map[string]*service),
		kills:    newKillHeap(),
		logger:   logger,
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

var (
	ErrTimeRewound         = errors.New("shutdown: operation time is earlier than a previously observed time")
	ErrInvalidGracePeriod  = errors.New("shutdown: grace period must be positive")
	ErrDuplicateService    = errors.New("shutdown: duplicate service")
	ErrUnknownDependency   = errors.New("shutdown: dependency is not registered")
	ErrDependencyCycle     = errors.New("shutdown: dependency cycle")
	ErrShutdownStarted     = errors.New("shutdown: cannot register after shutdown has started")
	ErrShutdownAlreadyDone = errors.New("shutdown: shutdown has already started")
	ErrServiceNotFound     = errors.New("shutdown: service not found")
	ErrAlreadyStopped      = errors.New("shutdown: service has already stopped")
	ErrNoTerminationSignal = errors.New("shutdown: service has not received a termination signal")
)

func (o *Orchestrator) Register(at int64, id string, gracePeriodMS int64, dependencies ...string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.logger.Info("register input", "at", at, "id", id, "grace_period_ms", gracePeriodMS, "dependencies", dependencies)
	if err := o.advanceTime(at); err != nil {
		o.logger.Info("register rejected", "id", id, "reason", err.Error())
		return err
	}
	if gracePeriodMS <= 0 {
		err := ErrInvalidGracePeriod
		o.logger.Info("register rejected", "id", id, "reason", err.Error())
		return err
	}
	if _, exists := o.services[id]; exists {
		err := ErrDuplicateService
		o.logger.Info("register rejected", "id", id, "reason", err.Error())
		return err
	}

	uniqueDependencies := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if dependency != id {
			if _, exists := o.services[dependency]; !exists {
				err := ErrUnknownDependency
				o.logger.Info("register rejected", "id", id, "dependency", dependency, "reason", err.Error())
				return err
			}
		}
		uniqueDependencies[dependency] = struct{}{}
	}
	if o.hasDependencyCycle(id, uniqueDependencies) {
		err := ErrDependencyCycle
		o.logger.Info("register rejected", "id", id, "reason", err.Error())
		return err
	}
	if o.started {
		err := ErrShutdownStarted
		o.logger.Info("register rejected", "id", id, "reason", err.Error())
		return err
	}

	registered := &service{
		id:            id,
		gracePeriodMS: gracePeriodMS,
		dependencies:  uniqueDependencies,
		dependents:    make(map[string]struct{}),
	}
	o.services[id] = registered
	o.order = append(o.order, id)
	for dependency := range uniqueDependencies {
		if dependency != id {
			o.services[dependency].dependents[id] = struct{}{}
		}
	}

	o.logger.Info("register output", "id", id, "registered", true)
	return nil
}

func (o *Orchestrator) BeginShutdown(at int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.logger.Info("begin shutdown input", "at", at)
	if err := o.advanceTime(at); err != nil {
		o.logger.Info("begin shutdown rejected", "reason", err.Error())
		return err
	}
	o.settleUntil(at)
	if o.started {
		err := ErrShutdownAlreadyDone
		o.logger.Info("begin shutdown rejected", "reason", err.Error())
		return err
	}

	o.started = true
	for _, id := range o.order {
		svc := o.services[id]
		if len(svc.dependents) == 0 {
			o.terminateService(svc, at)
		}
	}
	o.logger.Info("begin shutdown output", "t0", at)
	return nil
}

func (o *Orchestrator) ReportExit(id string, at int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.logger.Info("report exit input", "id", id, "at", at)
	if err := o.advanceTime(at); err != nil {
		o.logger.Info("report exit rejected", "id", id, "reason", err.Error())
		return err
	}
	o.settleUntil(at)

	svc := o.services[id]
	if svc == nil {
		err := ErrServiceNotFound
		o.logger.Info("report exit rejected", "id", id, "reason", err.Error())
		return err
	}
	if svc.stopped {
		err := ErrAlreadyStopped
		o.logger.Info("report exit rejected", "id", id, "stop_at", svc.stoppedAt, "reason", err.Error())
		return err
	}
	if !svc.terminated {
		err := ErrNoTerminationSignal
		o.logger.Info("report exit rejected", "id", id, "reason", err.Error())
		return err
	}

	deadline := svc.terminationAt + svc.gracePeriodMS
	if at >= deadline {
		o.logger.Info("report exit accepted as killed", "id", id, "at", at, "deadline_at", deadline)
		o.stopService(svc, deadline, Killed)
		o.logger.Info("report exit output", "id", id, "accepted", false, "reason", "deadline report is a kill")
		return ErrAlreadyStopped
	}

	o.logger.Info("report exit accepted as graceful", "id", id, "at", at, "deadline_at", deadline)
	o.stopService(svc, at, Graceful)
	o.logger.Info("report exit output", "id", id, "accepted", true)
	return nil
}

func (o *Orchestrator) Status(id string, at int64) (ServiceState, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.logger.Info("status input", "id", id, "at", at)
	if err := o.advanceTime(at); err != nil {
		o.logger.Info("status rejected", "id", id, "reason", err.Error())
		return ServiceState{}, err
	}
	o.settleUntil(at)

	svc := o.services[id]
	if svc == nil {
		err := ErrServiceNotFound
		o.logger.Info("status rejected", "id", id, "reason", err.Error())
		return ServiceState{}, err
	}

	state := o.serviceState(svc)
	o.logger.Info("status output", "id", id, "state", state)
	return state, nil
}

func (o *Orchestrator) Snapshot(at int64) (map[string]ServiceState, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.logger.Info("snapshot input", "at", at)
	if err := o.advanceTime(at); err != nil {
		o.logger.Info("snapshot rejected", "reason", err.Error())
		return nil, err
	}
	o.settleUntil(at)

	states := make(map[string]ServiceState, len(o.services))
	for _, id := range o.order {
		states[id] = o.serviceState(o.services[id])
	}
	o.logger.Info("snapshot output", "service_count", len(states))
	return states, nil
}

func (o *Orchestrator) advanceTime(at int64) error {
	if o.hasLastAt && at < o.lastAt {
		return ErrTimeRewound
	}
	o.hasLastAt = true
	o.lastAt = at
	return nil
}

func (o *Orchestrator) serviceState(svc *service) ServiceState {
	state := ServiceState{
		ID:            svc.id,
		GracePeriodMS: svc.gracePeriodMS,
		StopMethod:    NotStopped,
	}
	if svc.terminated {
		state.TerminationTime = svc.terminationAt
	}
	if svc.stopped {
		state.StopTime = svc.stoppedAt
		state.StopMethod = svc.stopMethod
	}
	return state
}

func (o *Orchestrator) hasDependencyCycle(candidateID string, candidateDependencies map[string]struct{}) bool {
	visiting := make(map[string]bool)
	var visit func(id string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		visiting[id] = true
		defer delete(visiting, id)

		registered := o.services[id]
		for dependency := range registered.dependencies {
			if dependency == candidateID || visit(dependency) {
				return true
			}
		}
		return false
	}
	for dependency := range candidateDependencies {
		if dependency == candidateID || visit(dependency) {
			return true
		}
	}
	return false
}
