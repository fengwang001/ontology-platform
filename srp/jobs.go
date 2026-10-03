package srp

// Start 是 SRP 启动闸门。
//
// 通过条件（按拒绝优先级依次判定）：
//  1. jobID/taskID 非空；
//  2. 任务存在、作业编号未重复；
//  3. 运行中作业数未达 32；
//  4. 任务 π 严格大于栈顶作业所属任务的 π（栈空无此要求）；
//  5. 任务 π 严格大于当前 SysCeil。
//
// 通过后作业压栈，成为新栈顶；之后其取放资源不再被他人阻塞。
func (s *SRP) Start(jobID, taskID string) error {
	if jobID == "" || taskID == "" {
		return errInvalid("job id and task id must be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok {
		return errNotFound("task %q does not exist", taskID)
	}
	if _, ok := s.jobs[jobID]; ok {
		return errDuplicate("job %q already running", jobID)
	}
	if len(s.stack) >= maxJobs {
		return errCapacity("running job capacity %d reached", maxJobs)
	}
	pi := s.levelLocked(t)
	if len(s.stack) > 0 {
		top := s.stack[len(s.stack)-1]
		topPi := s.levelLocked(top.task)
		if pi <= topPi {
			return errf(ReasonPreemptionLevelLow,
				"job %q: task %q pi=%d not strictly greater than top task %q pi=%d",
				jobID, taskID, pi, top.task.id, topPi)
		}
	}
	if sys := s.sysCeilLocked(); pi <= sys {
		return errf(ReasonCeilingBlocked,
			"job %q: task %q pi=%d not strictly greater than SysCeil=%d",
			jobID, taskID, pi, sys)
	}
	j := &job{id: jobID, task: t, held: map[string]int{}}
	s.jobs[jobID] = j
	s.stack = append(s.stack, j)
	return nil
}

// Acquire 由栈顶作业取得资源 r 的 u 个单元。
// 判定顺序：参数 → 作业/资源存在 → 栈顶 → 不超 μ（含 μ=0）→ 不超 avail。
func (s *SRP) Acquire(jobID, r string, u int) error {
	if jobID == "" || r == "" {
		return errInvalid("job id and resource id must be non-empty")
	}
	if u < 1 || u > 1000 {
		return errInvalid("acquire units u=%d out of range [1,1000]", u)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return errNotFound("job %q does not exist", jobID)
	}
	res, ok := s.resources[r]
	if !ok {
		return errNotFound("resource %q does not exist", r)
	}
	if s.topLocked() != j {
		return errf(ReasonNotTopOfStack, "job %q is not the top of stack", jobID)
	}
	limit := j.task.mu[r]
	if j.held[r]+u > limit {
		return errf(ReasonOverClaim,
			"job %q: held=%d + u=%d exceeds mu=%d for resource %q",
			jobID, j.held[r], u, limit, r)
	}
	if u > res.avail {
		s.shortageRejections++
		return errf(ReasonUnitUnavailable,
			"job %q: u=%d exceeds avail=%d of resource %q",
			jobID, u, res.avail, r)
	}
	j.held[r] += u
	res.avail -= u
	return nil
}

// Release 由栈顶作业归还资源 r 的 u 个单元；归还量不得超过已持有量。
func (s *SRP) Release(jobID, r string, u int) error {
	if jobID == "" || r == "" {
		return errInvalid("job id and resource id must be non-empty")
	}
	if u < 1 || u > 1000 {
		return errInvalid("release units u=%d out of range [1,1000]", u)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return errNotFound("job %q does not exist", jobID)
	}
	res, ok := s.resources[r]
	if !ok {
		return errNotFound("resource %q does not exist", r)
	}
	if s.topLocked() != j {
		return errf(ReasonNotTopOfStack, "job %q is not the top of stack", jobID)
	}
	if u > j.held[r] {
		return errf(ReasonNotHeld,
			"job %q: release u=%d exceeds held=%d of resource %q",
			jobID, u, j.held[r], r)
	}
	j.held[r] -= u
	res.avail += u
	return nil
}

// Finish 要求作业位于栈顶且不持有任何资源，随后弹栈。
func (s *SRP) Finish(jobID string) error {
	if jobID == "" {
		return errInvalid("job id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return errNotFound("job %q does not exist", jobID)
	}
	if s.topLocked() != j {
		return errf(ReasonNotTopOfStack, "job %q is not the top of stack", jobID)
	}
	for r, h := range j.held {
		if h > 0 {
			return errf(ReasonStillHolding,
				"job %q still holds %d unit(s) of resource %q", jobID, h, r)
		}
	}
	s.stack = s.stack[:len(s.stack)-1]
	delete(s.jobs, jobID)
	return nil
}

func (s *SRP) topLocked() *job {
	if len(s.stack) == 0 {
		return nil
	}
	return s.stack[len(s.stack)-1]
}

// Stack 返回自栈底到栈顶的作业快照（编号与所属任务）。
func (s *SRP) Stack() []JobInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobInfo, 0, len(s.stack))
	for _, j := range s.stack {
		out = append(out, JobInfo{ID: j.id, TaskID: j.task.id})
	}
	return out
}

// shortageRejectionsLocked 暴露非导出断言计数器供包内测试读取。
// 对任何遵守 μ 声明的合法操作序列，该计数器恒为 0（“单元不足”永不发生）。
func (s *SRP) shortageRejectionsLocked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shortageRejections
}
