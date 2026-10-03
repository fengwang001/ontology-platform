package runner

// Launch 启动工作流实例。
//
// inst 非空、def 为已登记定义、p 非空；now 合法。此刻记录启动快照
// E = grants(p) & ceil（对权限表恰读一次），步骤初始全部 Pending，
// 实例处于「无在跑步骤且仍有 Pending」状态。Launch 本身不执行任何步骤。
func (r *Runner) Launch(inst, def, p []byte, now int64) error {
	if len(inst) == 0 || len(def) == 0 || len(p) == 0 {
		return ErrArg
	}
	key := string(inst)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkClock(now); err != nil {
		return err
	}
	if _, ok := r.instMap[key]; ok {
		return ErrExists
	}
	d, ok := r.catalog.Get(def)
	if !ok {
		return ErrNotFound
	}
	// 快照只取这一次实时权限；之后新增授权被 E 封顶，撤销立即生效。
	snapshot := r.registry.Mask(p) & d.Ceil
	in := &instance{
		trigger:  append([]byte(nil), p...),
		ceil:     d.Ceil,
		reqs:     d.Reqs,
		snapshot: snapshot,
		steps:    make([]StepPhase, len(d.Reqs)),
		next:     0,
		phase:    PendingPhase,
	}
	r.instMap[key] = in
	r.clock = now
	return nil
}

// StartStep 尝试启动下一个 Pending 步骤。
//
// 前置：实例不处于终局、无 Running 步骤且仍有 Pending 步骤（Suspended 时
// 也允许重新尝试）。有效权限 eff = grants(p) 实时值 & E：授权取实时值、
// 但被启动快照封顶。req 是 eff 子集则 Allow，步骤 Running；否则 Deny（成功
// 结果而非错误），实例 Suspended，记录 miss 与到期时刻 now+T。
// 对 grants 恰读一次。
func (r *Runner) StartStep(inst []byte, now int64) error {
	if len(inst) == 0 {
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
	// 本体前置按到期前状态判定：仅「无在跑且仍有 Pending」可尝试。
	if in.phase != PendingPhase {
		return ErrState
	}
	if in.runningIndex() >= 0 || in.next >= len(in.reqs) {
		return ErrState
	}
	if r.expireIfDue(in, now, true) {
		// 恰等到期：Expire 先落库（now 的纯函数），本体不再执行。
		r.clock = now
		return ErrState
	}
	i := in.next
	eff := r.registry.Mask(in.trigger) & in.snapshot
	req := in.reqs[i]
	if req&^eff == 0 {
		in.steps[i] = StepRunning
		in.next = i + 1
		in.suspended = false
		in.miss = 0
		in.deadline = 0
		in.phase = RunningPhase
		in.append(Event{Kind: Allow, Step: i, At: now})
	} else {
		in.suspended = true
		in.miss = req &^ eff
		in.deadline = now + r.timeout
		in.append(Event{Kind: Deny, Step: i, Miss: in.miss, At: now})
	}
	r.clock = now
	return nil
}

// FinishStep 把当前 Running 步骤置 Done，不重新检查权限。
// 若全部步骤 Done，实例终局 Completed，终局时刻 now；否则回到可启动状态。
func (r *Runner) FinishStep(inst []byte, now int64) error {
	if len(inst) == 0 {
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
	if r.expireIfDue(in, now, true) {
		r.clock = now
		return ErrState
	}
	i := in.runningIndex()
	if in.phase != RunningPhase || i < 0 {
		return ErrState
	}
	in.steps[i] = StepDone
	in.phase = PendingPhase
	if in.next >= len(in.reqs) {
		in.phase = CompletedPhase
		in.outcome = Completed
		in.terminalAt = now
	}
	r.clock = now
	return nil
}

// runningIndex 返回 Running 步骤下标；不存在返回 -1。调用方须持锁。
func (in *instance) runningIndex() int {
	for i, sp := range in.steps {
		if sp == StepRunning {
			return i
		}
	}
	return -1
}
