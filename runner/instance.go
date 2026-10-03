package runner

// Event 是一条审计记录。每个成功的状态变更恰写一条，
// 序号在实例内从 1 连续。
type Event struct {
	// Seq 是实例内连续序号（从 1 开始）。
	Seq int
	// Kind 是条目类型（Allow/Deny/Override/Reject/Expire）。
	Kind Kind
	// Step 是关联的步骤下标（从 0 开始）。
	Step int
	// At 是该变更的生效时刻（Expire 时为到期时刻，其余为操作 now）。
	At int64
	// Miss 仅对 Deny 有效：req 去掉 eff 的缺失位。
	Miss uint64
	// Approver 仅对 Override/Reject 有效：审批人身份副本。
	Approver []byte
}

// instance 是工作流实例的全部可变状态。Runner 持锁访问。
type instance struct {
	trigger    []byte      // 触发者 p（副本）
	ceil       uint64      // 工作流声明上限
	reqs       []uint64    // 各步骤需求掩码（副本）
	snapshot   uint64      // E：Launch 时刻 grants(p)&ceil
	steps      []StepPhase // 各步骤状态
	next       int         // 下一个 Pending 步骤下标
	phase      Phase       // 实例阶段
	suspended  bool        // 是否挂起等待审批
	deadline   int64       // 挂起到期时刻
	miss       uint64      // 当前缺失位（req 去掉 eff）
	outcome    Outcome     // 终局类别
	terminalAt int64       // 终局时刻
	audit      []Event     // 审计日志
}

func (e Event) clone() Event {
	cp := e
	if e.Approver != nil {
		cp.Approver = append([]byte(nil), e.Approver...)
	}
	return cp
}

// append 追加一条审计，序号由实例内连续编号自动填充。调用方须填好其余字段。
func (in *instance) append(ev Event) {
	ev.Seq = len(in.audit) + 1
	if ev.Approver != nil {
		ev.Approver = append([]byte(nil), ev.Approver...)
	}
	in.audit = append(in.audit, ev)
}

// Status 是 Status 查询的只读结果；Suspended 且已到期时按 now 虚拟为终局。
type Status struct {
	Phase      Phase
	Steps      []StepPhase
	Next       int     // 下一个 Pending 步骤下标；无则 -1
	Suspended  bool    // 当前是否挂起（已按 now 虚拟到期处理）
	Miss       uint64  // 当前缺失位；非挂起为 0
	Deadline   int64   // 挂起到期时刻；未挂起为 0
	Outcome    Outcome // 终局类别；未终局为 None
	TerminalAt int64   // 终局时刻；未终局为 0
}

// statusLocked 构造状态投影。due 为 true 时把挂起实例按 now 虚拟成 Failed(Expired)。
// 调用方须持 Runner 锁。
func (in *instance) statusLocked(now int64) Status {
	st := Status{
		Phase:      in.phase,
		Steps:      append([]StepPhase(nil), in.steps...),
		Next:       in.next,
		Suspended:  in.suspended,
		Miss:       in.miss,
		Deadline:   in.deadline,
		Outcome:    in.outcome,
		TerminalAt: in.terminalAt,
	}
	if in.phase == PendingPhase && in.suspended && now >= in.deadline {
		st.Phase = FailedPhase
		st.Suspended = false
		st.Miss = 0
		st.Outcome = Expired
		st.TerminalAt = in.deadline
	}
	if st.Phase != PendingPhase && st.Outcome == None {
		st.Next = -1
	}
	if st.Phase == CompletedPhase {
		st.Outcome = Completed
	}
	return st
}

// AuditLog 返回实例审计日志的深拷贝（按序号升序）。
// 实例不存在时返回 ErrNotFound。
func (r *Runner) AuditLog(inst []byte) ([]Event, error) {
	if len(inst) == 0 {
		return nil, ErrArg
	}
	r.mu.Lock()
	in, ok := r.instMap[string(inst)]
	if !ok {
		r.mu.Unlock()
		return nil, ErrNotFound
	}
	out := make([]Event, len(in.audit))
	for i, ev := range in.audit {
		out[i] = ev.clone()
	}
	r.mu.Unlock()
	return out, nil
}
