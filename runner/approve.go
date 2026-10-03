package runner

import "ontology/grants"

// Approve 由审批人 a 放行当前挂起的步骤。
//
// 实例须为 Suspended；到期处理先于本体（已 Failed 返回 ErrState，且先于
// ErrSelf/ErrNoAuthority）。a 不得是触发者本人（ErrSelf）；a 此刻掩码须含
// 位 63（ErrNoAuthority）。通过后仅当前一步 Running，不受 miss 约束；
// 后续各步仍按 eff 判定。
func (r *Runner) Approve(inst, a []byte, now int64) error {
	return r.resolve(inst, a, now, true)
}

// Reject 由审批人 a 拒绝挂起的步骤。资格与判定次序同 Approve；
// 通过后实例终局 Failed(Rejected)，终局时刻 now。
func (r *Runner) Reject(inst, a []byte, now int64) error {
	return r.resolve(inst, a, now, false)
}

func (r *Runner) resolve(inst, a []byte, now int64, approve bool) error {
	if len(inst) == 0 || len(a) == 0 {
		return ErrArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkClock(now); err != nil {
		return err
	}
	in, ok := r.instMap[string(inst)]
	if !ok {
		return ErrNotFound
	}
	// 到期先处理：处理后已终局则一切后续资格都不再判定。
	if r.expireIfDue(in, now, true) {
		r.clock = now
		return ErrState
	}
	if in.phase != PendingPhase || !in.suspended {
		return ErrState
	}
	if string(a) == string(in.trigger) {
		return ErrSelf // 自批先于无权判定
	}
	if r.registry.Mask(a)&grants.ApproverBit == 0 {
		return ErrNoAuthority
	}
	i := in.next
	if approve {
		in.steps[i] = StepRunning
		in.next = i + 1
		in.suspended = false
		in.miss = 0
		in.deadline = 0
		in.phase = RunningPhase
		in.append(Event{Kind: Override, Step: i, At: now, Approver: a})
	} else {
		in.suspended = false
		in.miss = 0
		in.deadline = 0
		in.phase = FailedPhase
		in.outcome = Rejected
		in.terminalAt = now
		in.append(Event{Kind: RejectEv, Step: i, At: now, Approver: a})
	}
	r.clock = now
	return nil
}

// Status 只读返回实例按 now 虚拟处理后的状态：若已到挂起到期时刻，
// 投影为 Failed(Expired)，但不落库、不推进时钟。
//
// now 仍须合法且不小于全局时钟（ErrClock）；实例不存在返回 ErrNotFound。
func (r *Runner) Status(inst []byte, now int64) (Status, error) {
	if len(inst) == 0 {
		return Status{}, ErrArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkClock(now); err != nil {
		return Status{}, err
	}
	in, ok := r.instMap[string(inst)]
	if !ok {
		return Status{}, ErrNotFound
	}
	return in.statusLocked(now), nil
}
