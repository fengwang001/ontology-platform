package srp

// levelLocked 重新推出当前全部任务的抢占层级 π。
// 互不相同的 D 值从大到小排序，D 最大者 π=1；D 相同则 π 相同。
// 调用方必须持有 s.mu。
func (s *SRP) levelLocked(t *task) int {
	return countDistinctGreaterLocked(s, t.d) + 1
}

// countDistinctGreaterLocked 统计严格大于 d 的互不相同 D 值个数。
func countDistinctGreaterLocked(s *SRP, d int) int {
	seen := map[int]struct{}{}
	for _, other := range s.tasks {
		if other.d > d {
			seen[other.d] = struct{}{}
		}
	}
	return len(seen)
}

// ceilLocked 返回资源 r 的天花板：max{π_k : μ_{k,r} > avail_r}，无则 0。
// 严格不等号：μ 恰等于 avail 不计入天花板。
// 调用方必须持有 s.mu。
func (s *SRP) ceilLocked(r *resource) int {
	c := 0
	for _, t := range s.tasks {
		if t.mu[r.id] > r.avail {
			if pi := s.levelLocked(t); pi > c {
				c = pi
			}
		}
	}
	return c
}

// sysCeilLocked 返回各资源天花板的最大值；无资源时为 0。
func (s *SRP) sysCeilLocked() int {
	c := 0
	for _, r := range s.resources {
		if v := s.ceilLocked(r); v > c {
			c = v
		}
	}
	return c
}

// DeclareResource 声明编号为 id、总单元数为 n 的多单元资源，余量初值为 n。
func (s *SRP) DeclareResource(id string, n int) error {
	if id == "" {
		return errInvalid("resource id is empty")
	}
	if n < 1 || n > 1000 {
		return errInvalid("resource %q total units N=%d out of range [1,1000]", id, n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.resources[id]; ok {
		return errDuplicate("resource %q already declared", id)
	}
	if len(s.resources) >= maxResources {
		return errCapacity("resource capacity %d reached", maxResources)
	}
	s.resources[id] = &resource{id: id, n: n, avail: n}
	return nil
}

// AddTask 增加任务。mus 为 (资源, μ) 列表，至多 4 项；未列出的资源 μ=0。
func (s *SRP) AddTask(id string, d int, mus ...Mu) error {
	if id == "" {
		return errInvalid("task id is empty")
	}
	if d < 1 || d > 1_000_000 {
		return errInvalid("task %q relative deadline D=%d out of range [1,10^6]", id, d)
	}
	if len(mus) > 4 {
		return errInvalid("task %q declares %d resources, at most 4", id, len(mus))
	}
	seen := map[string]struct{}{}
	for _, m := range mus {
		if m.Resource == "" {
			return errInvalid("task %q has empty resource id", id)
		}
		if _, dup := seen[m.Resource]; dup {
			return errInvalid("task %q declares resource %q more than once", id, m.Resource)
		}
		seen[m.Resource] = struct{}{}
		if m.Units < 1 {
			return errInvalid("task %q mu for %q is %d, must be >= 1", id, m.Resource, m.Units)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; ok {
		return errDuplicate("task %q already exists", id)
	}
	if len(s.tasks) >= maxTasks {
		return errCapacity("task capacity %d reached", maxTasks)
	}
	for _, m := range mus {
		r, ok := s.resources[m.Resource]
		if !ok {
			return errNotFound("task %q references undeclared resource %q", id, m.Resource)
		}
		if m.Units > r.n {
			return errInvalid("task %q mu=%d exceeds resource %q N=%d", id, m.Units, m.Resource, r.n)
		}
	}
	mu := make(map[string]int, len(mus))
	for _, m := range mus {
		mu[m.Resource] = m.Units
	}
	s.tasks[id] = &task{id: id, d: d, mu: mu}
	return nil
}

// RemoveTask 删除任务；该任务仍有未结束作业时返回 ReasonTaskInUse。
func (s *SRP) RemoveTask(id string) error {
	if id == "" {
		return errInvalid("task id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return errNotFound("task %q does not exist", id)
	}
	for _, j := range s.stack {
		if j.task == t {
			return errf(ReasonTaskInUse, "task %q has %d unfinished job(s)", id, countJobsForLocked(s, t))
		}
	}
	delete(s.tasks, id)
	return nil
}

func countJobsForLocked(s *SRP, t *task) int {
	n := 0
	for _, j := range s.stack {
		if j.task == t {
			n++
		}
	}
	return n
}

// Ceil 返回资源 r 当前的天花板。
func (s *SRP) Ceil(r string) (int, error) {
	if r == "" {
		return 0, errInvalid("resource id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.resources[r]
	if !ok {
		return 0, errNotFound("resource %q does not exist", r)
	}
	return s.ceilLocked(res), nil
}

// SysCeil 返回当前系统天花板。
func (s *SRP) SysCeil() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sysCeilLocked()
}

// Avail 返回资源 r 的当前余量。
func (s *SRP) Avail(r string) (int, error) {
	if r == "" {
		return 0, errInvalid("resource id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.resources[r]
	if !ok {
		return 0, errNotFound("resource %q does not exist", r)
	}
	return res.avail, nil
}

// Level 返回任务 t 当前的抢占层级 π；任务增删后自动重排。
func (s *SRP) Level(t string) (int, error) {
	if t == "" {
		return 0, errInvalid("task id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[t]
	if !ok {
		return 0, errNotFound("task %q does not exist", t)
	}
	return s.levelLocked(task), nil
}
