package podstate

func reject(r RejectReason, msg string) error {
	return &RejectError{Reason: r, msg: msg}
}

// New validates the configuration and constructs a new machine.
func New(cfg Config) (*Pod, error) {
	if cfg.InitContainers < 0 || cfg.InitContainers > 16 {
		return nil, reject(RejectInvalidArgument, "init container count out of range [0,16]")
	}
	if cfg.AppContainers < 1 || cfg.AppContainers > 16 {
		return nil, reject(RejectInvalidArgument, "app container count out of range [1,16]")
	}
	if cfg.RestartPolicy < Always || cfg.RestartPolicy > Never {
		return nil, reject(RejectInvalidArgument, "restart policy out of range")
	}
	if cfg.BackoffBase < 1 || cfg.BackoffMax < cfg.BackoffBase || cfg.BackoffMax > 1e12 {
		return nil, reject(RejectInvalidArgument, "backoff parameters must satisfy 1 <= B <= M <= 1e12")
	}
	if cfg.BackoffReset < 1 || cfg.BackoffReset > 1e12 {
		return nil, reject(RejectInvalidArgument, "backoff reset duration out of range")
	}
	if cfg.ActiveDeadline < 0 || cfg.ActiveDeadline > 1e12 {
		return nil, reject(RejectInvalidArgument, "active deadline out of range")
	}
	if cfg.RestartBudget < 0 || cfg.RestartBudget > 1e6 {
		return nil, reject(RejectInvalidArgument, "restart budget out of range")
	}
	n := cfg.InitContainers + cfg.AppContainers
	p := &Pod{
		cfg: cfg,
		cs:  make([]container, n),
	}
	return p, nil
}

func (p *Pod) isInit(c int) bool { return c < p.cfg.InitContainers }

// backoffDelay returns min(B*2^k, M) without any integer overflow.
func backoffDelay(base int64, k int, max int64) int64 {
	d := base
	for i := 0; i < k; i++ {
		if d >= max || d >= (1<<62) {
			return max
		}
		d <<= 1
		if d >= max {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

// Start moves a Waiting container into Running when all gates pass.
func (p *Pod) Start(c int, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c < 0 || c >= len(p.cs) || now < 0 {
		return reject(RejectInvalidArgument, "start: invalid container index or negative now")
	}
	if now < p.maxNow {
		return reject(RejectClockWentBack, "start: clock went backwards")
	}
	cs := &p.cs[c]
	if cs.state != Waiting {
		return reject(RejectStartNotWaiting, "start: container is not Waiting")
	}
	if p.hasT0 && p.cfg.ActiveDeadline > 0 && now-p.t0 >= p.cfg.ActiveDeadline {
		return reject(RejectDeadlineExceeded, "start: active deadline exceeded")
	}
	if p.isInit(c) {
		for i := 0; i < c; i++ {
			if p.cs[i].state != Succeeded {
				return reject(RejectInitNotComplete, "start: preceding init container not Succeeded")
			}
		}
	} else {
		for i := 0; i < p.cfg.InitContainers; i++ {
			if p.cs[i].state != Succeeded {
				return reject(RejectInitNotComplete, "start: init containers not all Succeeded")
			}
		}
	}
	if now < cs.next {
		return reject(RejectBackingOff, "start: container is in crash-loop backoff")
	}

	cs.state = Running
	cs.startedAt = now
	cs.started = true
	if !p.hasT0 {
		p.hasT0 = true
		p.t0 = now
	}
	p.maxNow = now
	return nil
}

// Exit terminates a Running container and routes it per the restart policy.
func (p *Pod) Exit(c int, code, now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c < 0 || c >= len(p.cs) || now < 0 {
		return reject(RejectInvalidArgument, "exit: invalid container index or negative now")
	}
	if now < p.maxNow {
		return reject(RejectClockWentBack, "exit: clock went backwards")
	}
	cs := &p.cs[c]
	if cs.state != Running {
		return reject(RejectExitNotRunning, "exit: container is not Running")
	}

	ran := now - cs.startedAt
	policy := p.cfg.RestartPolicy

	restart := false
	if p.isInit(c) {
		if code == 0 {
			cs.state = Succeeded
		} else if policy == Always || policy == OnFailure {
			restart = true
		} else {
			cs.state = Failed
		}
	} else {
		if code == 0 {
			if policy == Always {
				restart = true
			} else {
				cs.state = Succeeded
			}
		} else if policy == Always || policy == OnFailure {
			restart = true
		} else {
			cs.state = Failed
		}
	}

	if restart && p.cfg.RestartBudget > 0 && p.restarts >= p.cfg.RestartBudget {
		// Budget exhausted: fall back to Never semantics without touching
		// fails/next/restarts.
		if code == 0 {
			cs.state = Succeeded
		} else {
			cs.state = Failed
		}
		restart = false
	}

	if restart {
		if ran >= p.cfg.BackoffReset {
			cs.fails = 0
		}
		delay := backoffDelay(p.cfg.BackoffBase, cs.fails, p.cfg.BackoffMax)
		cs.next = now + delay
		cs.fails++
		p.restarts++
		cs.state = Waiting
	}

	p.maxNow = now
	return nil
}

func (p *Pod) deadlineFailed(now int64) bool {
	return p.cfg.ActiveDeadline > 0 && p.hasT0 && now-p.t0 >= p.cfg.ActiveDeadline
}

// phaseLocked derives the phase; caller must hold p.mu.
func (p *Pod) phaseLocked(now int64) Phase {
	if p.deadlineFailed(now) {
		return PhaseFailed
	}
	I := p.cfg.InitContainers
	for i := 0; i < I; i++ {
		if p.cs[i].state == Failed {
			return PhaseFailed
		}
	}
	allTerminal := true
	anyAppFailed := false
	anyAppStarted := false
	for i := I; i < len(p.cs); i++ {
		switch p.cs[i].state {
		case Succeeded:
		case Failed:
			anyAppFailed = true
		default:
			allTerminal = false
		}
		if p.cs[i].started {
			anyAppStarted = true
		}
	}
	if allTerminal {
		if anyAppFailed {
			return PhaseFailed
		}
		return PhaseSucceeded
	}
	for i := 0; i < I; i++ {
		if p.cs[i].state != Succeeded {
			return PhasePending
		}
	}
	if !anyAppStarted {
		return PhasePending
	}
	return PhaseRunning
}

// Phase derives the Pod phase at time now.
func (p *Pod) Phase(now int64) (Phase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < 0 {
		return PhasePending, reject(RejectInvalidArgument, "phase: negative now")
	}
	return p.phaseLocked(now), nil
}

// Reason reports the human-facing container reason at time now.
func (p *Pod) Reason(c int, now int64) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c < 0 || c >= len(p.cs) || now < 0 {
		return "", reject(RejectInvalidArgument, "reason: invalid container index or negative now")
	}
	cs := &p.cs[c]
	if cs.state != Succeeded && cs.state != Failed && p.deadlineFailed(now) {
		return "DeadlineExceeded", nil
	}
	if cs.state == Waiting && now < cs.next {
		return "CrashLoopBackOff", nil
	}
	return cs.state.String(), nil
}

// Container returns a snapshot of container c.
func (p *Pod) Container(c int) (ContainerSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c < 0 || c >= len(p.cs) {
		return ContainerSnapshot{}, reject(RejectInvalidArgument, "container: invalid index")
	}
	cs := &p.cs[c]
	return ContainerSnapshot{
		State:   cs.state,
		Fails:   cs.fails,
		Next:    cs.next,
		Started: cs.started,
	}, nil
}

// Restarts returns the global restart counter.
func (p *Pod) Restarts() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restarts
}

// T0 returns the timing origin and whether it has been set.
func (p *Pod) T0() (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.t0, p.hasT0
}
