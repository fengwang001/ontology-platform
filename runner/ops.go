package runner

import "ontology/grants"

// Launch 以触发者 p 启动实例，记录快照 E = grants(p) & ceil。
func (r *Runner) Launch(inst, def, p string, now int64) error {
	if inst == "" || def == "" || p == "" {
		return ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNow(now); err != nil {
		return err
	}
	d, ok := r.flows.Get(def)
	if !ok {
		return ErrNotFound
	}
	if _, dup := r.insts[inst]; dup {
		return ErrExists
	}
	r.insts[inst] = &instance{
		principal: p,
		snapshot:  r.grants.Mask(p) & d.Ceil,
		reqs:      append([]uint64(nil), d.Reqs...),
		steps:     make([]StepState, len(d.Reqs)),
		running:   -1,
		state:     StateActive,
	}
	r.clock = now
	return nil
}

// StartStep 取下一个 Pending 步骤：req 是 eff 的子集则 Running（记
// Allow），否则实例 Suspended（记 Deny(miss)，到期时刻 now+T）。
func (r *Runner) StartStep(inst string, now int64) (StepOutcome, error) {
	if inst == "" {
		return StepOutcome{}, ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNow(now); err != nil {
		return StepOutcome{}, err
	}
	in, ok := r.insts[inst]
	if !ok {
		return StepOutcome{}, ErrNotFound
	}
	in.expireIfDue(now)
	i := in.nextPending()
	if in.state != StateActive || in.running != -1 || i == -1 {
		return StepOutcome{}, ErrState
	}
	eff := r.grants.Mask(in.principal) & in.snapshot // 对 grants 恰读一次
	req := in.reqs[i]
	if miss := req &^ eff; miss != 0 {
		in.state = StateSuspended
		in.miss = miss
		in.deadline = now + r.timeout
		in.record(EvDeny, i, miss, "", now)
		r.clock = now
		return StepOutcome{Index: i, Miss: miss}, nil
	}
	in.steps[i] = StepRunning
	in.running = i
	in.record(EvAllow, i, 0, "", now)
	r.clock = now
	return StepOutcome{Index: i, Allowed: true}, nil
}

// FinishStep 把 Running 步骤置 Done，不重新检查权限；全部 Done 则
// 实例 Completed。返回完成的步骤下标。
func (r *Runner) FinishStep(inst string, now int64) (int, error) {
	if inst == "" {
		return -1, ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNow(now); err != nil {
		return -1, err
	}
	in, ok := r.insts[inst]
	if !ok {
		return -1, ErrNotFound
	}
	in.expireIfDue(now)
	if in.state != StateActive || in.running == -1 {
		return -1, ErrState
	}
	i := in.running
	in.steps[i] = StepDone
	in.running = -1
	if in.nextPending() == -1 {
		in.state = StateCompleted
	}
	r.clock = now
	return i, nil
}

// Approve 由有审批位的第三人放行被挂起实例的当前步骤（仅这一步越权）。
func (r *Runner) Approve(inst, a string, now int64) error {
	return r.decide(inst, a, now, true)
}

// Reject 由有审批位的第三人拒绝，实例终局 Failed(Rejected)。
func (r *Runner) Reject(inst, a string, now int64) error {
	return r.decide(inst, a, now, false)
}

func (r *Runner) decide(inst, a string, now int64, approve bool) error {
	if inst == "" || a == "" {
		return ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNow(now); err != nil {
		return err
	}
	in, ok := r.insts[inst]
	if !ok {
		return ErrNotFound
	}
	in.expireIfDue(now)
	if in.state != StateSuspended {
		return ErrState
	}
	if a == in.principal {
		return ErrSelf
	}
	if r.grants.Mask(a)&grants.ApproveBit == 0 {
		return ErrNoAuthority
	}
	i := in.nextPending()
	if approve {
		in.steps[i] = StepRunning
		in.running = i
		in.state = StateActive
		in.miss = 0
		in.record(EvOverride, i, 0, a, now)
	} else {
		in.state = StateFailed
		in.term = TermRejected
		in.termAt = now
		in.record(EvReject, i, 0, a, now)
	}
	r.clock = now
	return nil
}

// Status 只读返回按 now 虚拟做到期处理后的视图，不推进时钟、不落审计。
func (r *Runner) Status(inst string, now int64) (StatusView, error) {
	if inst == "" {
		return StatusView{}, ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkNow(now); err != nil {
		return StatusView{}, err
	}
	in, ok := r.insts[inst]
	if !ok {
		return StatusView{}, ErrNotFound
	}
	v := StatusView{
		State:    in.state,
		Steps:    append([]StepState(nil), in.steps...),
		Miss:     in.miss,
		Deadline: in.deadline,
		Term:     in.term,
		TermAt:   in.termAt,
	}
	if in.state == StateSuspended && in.deadline <= now {
		v.State = StateFailed
		v.Term = TermExpired
		v.TermAt = in.deadline
	}
	return v, nil
}

// Audit 返回实例的审计日志副本。
func (r *Runner) Audit(inst string) ([]Event, error) {
	if inst == "" {
		return nil, ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.insts[inst]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]Event(nil), in.audit...), nil
}
