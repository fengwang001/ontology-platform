package mcsched

import "sync"

// Level 为任务关键级。
type Level int

const (
	LO Level = iota
	HI
)

// Mode 为系统运行模式。
type Mode int

const (
	ModeLO Mode = iota
	ModeHI
)

// Task 是静态任务参数。
type Task struct {
	ID   string
	Lvl  Level
	CL   int // 低关键级预算
	CH   int // 高关键级预算
	T    int // 周期（相对截止期等于 T）
	Prio int // 固定优先级，数值小者优先
	Phi  int // 首次释放时刻
}

// Stats 汇总运行期计数，字段顺序即题目给定的报告顺序。
type Stats struct {
	Switches    int // LO -> HI 切换次数
	Recoveries  int // HI -> LO 恢复次数
	Discarded   int // 切换时被舍弃的 LO 作业个数
	Skipped     int // HI 模式下被跳过的 LO 作业释放次数
	MissedLO    int // LO 作业错过截止期次数
	MissedHI    int // HI 作业错过截止期次数
	Completions int // 作业完成次数
}

type taskSpec struct {
	t Task
}

// demand 返回该任务第 k 个作业的实际执行量；未显式设置时为 CL。
func (m *Monitor) demand(id string, k, cl int) int {
	if ks, ok := m.demands[id]; ok {
		if c, ok := ks[k]; ok {
			return c
		}
	}
	return cl
}

type job struct {
	task *taskSpec
	k    int // 作业序号（自 0 起，含被跳过的序号）
	d    int // 绝对截止期
	exec int // 已执行量
	rem  int // 剩余量
}

// Monitor 是混合关键级运行时模式监控器。
// 单个互斥锁串行化所有操作与查询，因此并发调用等价于某个串行顺序。
type Monitor struct {
	mu      sync.Mutex
	started bool
	tasks   []*taskSpec
	byID    map[string]*taskSpec
	prios   map[int]struct{}
	demands map[string]map[int]int

	now    int // 已处理的 tick 数，即下一 tick 的时刻
	mode   Mode
	active []*job
	stats  Stats
	trace  []string // trace[t] 为第 t 个 tick 运行的任务编号，空串表示空闲
}

// New 创建一个空监控器。
func New() *Monitor {
	return &Monitor{
		byID:    make(map[string]*taskSpec),
		prios:   make(map[int]struct{}),
		demands: make(map[string]map[int]int),
		mode:    ModeLO,
	}
}

// AddTask 在仿真开始前注册一个任务。
// 拒绝顺序：已开始 -> 参数非法 -> 编号重复 -> 优先级重复 -> 任务数已满。
func (m *Monitor) AddTask(t Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return ErrStarted
	}
	if !validTask(t) {
		return ErrInvalidParam
	}
	if _, ok := m.byID[t.ID]; ok {
		return ErrDuplicateID
	}
	if _, ok := m.prios[t.Prio]; ok {
		return ErrDuplicatePrio
	}
	if len(m.tasks) >= MaxTasks {
		return ErrTaskTableFull
	}

	spec := &taskSpec{t: t}
	m.tasks = append(m.tasks, spec)
	m.byID[t.ID] = spec
	m.prios[t.Prio] = struct{}{}
	return nil
}

// SetDemand 覆盖编号 id 的任务第 k 个作业（k 自 0 起）的实际执行量。
// 拒绝顺序：编号不存在 -> 参数非法 -> 该作业已释放。
func (m *Monitor) SetDemand(id string, k, c int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	spec, ok := m.byID[id]
	if !ok {
		return ErrUnknownTask
	}
	t := spec.t
	maxC := t.CH // LO 任务 CH==CL，故统一上界 CH
	if k < 0 || c < 1 || c > maxC {
		return ErrInvalidParam
	}
	release := t.Phi + k*t.T
	if release < m.now {
		return ErrJobReleased
	}

	ks := m.demands[id]
	if ks == nil {
		ks = make(map[int]int)
		m.demands[id] = ks
	}
	ks[k] = c // 重复设置以最后一次为准
	return nil
}

// Step 依次处理 n 个 tick。n 不在 [1, 10^6] 时返回 ErrInvalidParam。
func (m *Monitor) Step(n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if n < 1 || n > 1_000_000 {
		return ErrInvalidParam
	}
	m.started = true

	for i := 0; i < n; i++ {
		t := m.now

		// 第一步：d==t 的未完成作业记为错过并移除。
		var kept []*job
		for _, j := range m.active {
			if j.d == t {
				if j.task.t.Lvl == LO {
					m.stats.MissedLO++
				} else {
					m.stats.MissedHI++
				}
			} else {
				kept = append(kept, j)
			}
		}
		m.active = kept

		// 第二步：HI 模式且无任何未完成作业 -> 恢复 LO。
		if m.mode == ModeHI && len(m.active) == 0 {
			m.mode = ModeLO
			m.stats.Recoveries++
		}

		// 第三步：释放此刻到期的作业；HI 模式下 LO 任务的释放被丢弃（跳过）。
		for _, spec := range m.tasks {
			tp := spec.t
			if t < tp.Phi || (t-tp.Phi)%tp.T != 0 {
				continue
			}
			k := (t - tp.Phi) / tp.T
			if m.mode == ModeHI && tp.Lvl == LO {
				m.stats.Skipped++
				continue
			}
			c := m.demand(tp.ID, k, tp.CL)
			m.active = append(m.active, &job{
				task: spec,
				k:    k,
				d:    t + tp.T,
				exec: 0,
				rem:  c,
			})
		}

		// 选择 prio 最小的作业运行一个 tick。
		var cur *job
		curIdx := -1
		for idx, j := range m.active {
			if cur == nil || j.task.t.Prio < cur.task.t.Prio {
				cur, curIdx = j, idx
			}
		}

		if cur == nil {
			m.trace = append(m.trace, "")
		} else {
			cur.exec++
			cur.rem--
			m.trace = append(m.trace, cur.task.t.ID)

			if cur.rem == 0 {
				// 完成优先：恰在 exec==CL 完成时不触发切换。
				rest := make([]*job, 0, len(m.active)-1)
				rest = append(rest, m.active[:curIdx]...)
				rest = append(rest, m.active[curIdx+1:]...)
				m.active = rest
				m.stats.Completions++
			} else if m.mode == ModeLO && cur.task.t.Lvl == HI && cur.exec == cur.task.t.CL {
				// tick 末：本 tick 使已执行量恰达 CL 且作业未完成
				// （即实际执行量大于 CL）-> 切换 HI，舍弃所有未完成 LO 作业。
				m.mode = ModeHI
				m.stats.Switches++
				hiJobs := make([]*job, 0, len(m.active))
				for _, j := range m.active {
					if j.task.t.Lvl == LO {
						m.stats.Discarded++
					} else {
						hiJobs = append(hiJobs, j)
					}
				}
				m.active = hiJobs
			}
		}

		m.now = t + 1
	}
	return nil
}

// Mode 返回当前模式。
func (m *Monitor) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// Stats 返回当前全部计数的快照。
func (m *Monitor) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// Now 返回已处理的 tick 数（当前时刻）。
func (m *Monitor) Now() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// RunAt 返回第 t 个 tick（t 从 0 起）实际运行的任务编号；
// 该 tick 空闲或 t 超出/早于已模拟范围时返回空字符串与 false。
func (m *Monitor) RunAt(t int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t < 0 || t >= len(m.trace) || m.trace[t] == "" {
		return "", false
	}
	return m.trace[t], true
}
