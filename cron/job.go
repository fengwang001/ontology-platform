package cron

// maxMissed 是单次 Sync 允许补偿窗口内的最大触发个数。
const maxMissed = 100

// nextFireHook 供测试统计 Sync 内部对 NextFire 的调用次数。
var nextFireHook = func(parsed *cronSpec, t int64) (int64, error) {
	return nextFireParsed(parsed, t)
}

// NewJob 创建一个 CronJob 式补偿控制器。
// created 为创建时刻（初始 L 与 Sync 水位）；D 为截止窗口（-1 表示无截止）。
func NewJob(spec string, created int64, d int64, policy Policy) (*Job, error) {
	parsed, err := Parse(spec)
	if err != nil {
		return nil, ErrInvalidArg
	}
	if created < 0 || created > maxMinute {
		return nil, ErrInvalidArg
	}
	if d < -1 || d > 1_000_000_000 {
		return nil, ErrInvalidArg
	}
	if policy != Allow && policy != Forbid && policy != Replace {
		return nil, ErrInvalidArg
	}
	return &Job{
		spec:     parsed,
		deadline: d,
		policy:   policy,
		last:     created,
		water:    created,
		active:   make(map[int64]struct{}),
	}, nil
}

// SetSuspend 设置停摆标志；恢复本身不补偿任何触发。
func (j *Job) SetSuspend(b bool) {
	j.mu.Lock()
	j.suspend = b
	j.mu.Unlock()
}

// Sync 在 now 时刻检查 (L, now] 且不早于 now-D 的触发窗口。
// 只补窗口内最近一次触发；返回新建任务编号、是否真的新建与错误。
func (j *Job) Sync(now int64) (taskID int64, fired bool, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if now < 0 || now > maxMinute {
		return 0, false, ErrIllegalTime
	}
	prevWater := j.water
	if now < j.water {
		return 0, false, ErrClockBack
	}
	// 先推进水位：若随后因错过太多被拒绝，回滚到旧水位。
	j.water = now
	if j.suspend {
		return 0, false, nil
	}

	cutoff := int64(0)
	if j.deadline != -1 {
		cutoff = now - j.deadline
	}

	// 找窗口内首个触发：严格大于 max(L, cutoff-1) 的第一次触发，
	// 随后顺序计数；整体最多 101 次 NextFire（1 次定位 + 100 次后继）。
	base := j.last
	if j.deadline != -1 && cutoff-1 > base {
		base = cutoff - 1
	}
	first, err := j.nextFireAfter(base)
	if err != nil {
		if err == ErrNoNext {
			// 窗口内没有触发：无事发生，水位推进保留。
			return 0, false, nil
		}
		j.water = prevWater
		return 0, false, err
	}
	if first > now {
		return 0, false, nil
	}

	// 从首个窗口触发开始顺序计数，超过 100 即拒绝且不改任何状态。
	count := 0
	latest := first
	cur := first
	for cur <= now {
		count++
		if count > maxMissed {
			j.water = prevWater
			return 0, false, ErrTooMany
		}
		latest = cur
		next, nerr := j.nextFireAfter(cur)
		if nerr != nil {
			if nerr == ErrNoNext {
				break
			}
			j.water = prevWater
			return 0, false, nerr
		}
		cur = next
	}

	// 只补最近一次 t*。
	shouldFire := true
	if j.policy == Forbid && len(j.active) > 0 {
		shouldFire = false
		j.skipped++
	}
	if shouldFire && j.policy == Replace && len(j.active) > 0 {
		j.replaced += int64(len(j.active))
		j.active = make(map[int64]struct{})
	}
	if shouldFire {
		j.nextID++
		j.active[j.nextID] = struct{}{}
		taskID = j.nextID
		fired = true
	}
	j.last = latest
	return taskID, fired, nil
}

// nextFireAfter 在已解析 spec 上求严格大于 t 的下一次触发。
func (j *Job) nextFireAfter(t int64) (int64, error) {
	return nextFireHook(j.spec, t)
}

// Finish 把指定任务移出活动集合。
func (j *Job) Finish(taskID, now int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if now < 0 || now > maxMinute {
		return ErrIllegalTime
	}
	if _, ok := j.active[taskID]; !ok {
		return ErrNoSuchTask
	}
	delete(j.active, taskID)
	return nil
}

// Skipped 返回 Forbid 策略下累计跳过次数。
func (j *Job) Skipped() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.skipped
}

// Replaced 返回 Replace 策略下累计终止的任务数。
func (j *Job) Replaced() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.replaced
}

// Last 返回上次调度时刻 L。
func (j *Job) Last() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.last
}

// Water 返回上次成功 Sync 的时钟水位（主要用于测试与可观测）。
func (j *Job) Water() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.water
}

// Active 返回活动任务编号的升序快照。
func (j *Job) Active() []int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	ids := make([]int64, 0, len(j.active))
	for id := range j.active {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for k := i; k > 0 && ids[k-1] > ids[k]; k-- {
			ids[k-1], ids[k] = ids[k], ids[k-1]
		}
	}
	return ids
}
