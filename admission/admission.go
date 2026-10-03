package admission

import (
	"sort"
	"sync"
)

// PendingItem 描述某个时刻待处理作业的对外视图。
type PendingItem struct {
	ID      string
	Remain  int64
	Started bool
}

// RejectReason 区分拒绝原因，数值越小优先级越高（Accepted=0 表示接纳）。
type RejectReason int

const (
	Accepted          RejectReason = 0
	RejectInvalidArgs RejectReason = 1
	RejectClockBack   RejectReason = 2
	RejectDuplicateID RejectReason = 3
	RejectInfeasible  RejectReason = 4
	RejectOverload    RejectReason = 5
)

// SubmitResult 是一次 Submit 的结果。
type SubmitResult struct {
	Accepted bool
	Reason   RejectReason
	Evicted  []string
}

// Result 是一次 Advance 的结果。
type Result struct {
	OK     bool
	Reason RejectReason
}

// job 是控制器内部作业表示。pending 始终按 (deadline, id) 字节序升序排列。
type job struct {
	id        string
	remain    int64
	deadline  int64
	tolerance int64
	value     int64
	started   bool
}

// Controller 是单核过载接纳控制器。
type Controller struct {
	mu      sync.Mutex
	now     int64
	value   int64
	pending []*job
	evicted []string

	// 非导出计数器：供测试断言一次 Submit 内排序次数与可行性判定次数的上界。
	sorts            int
	feasibilityScans int
	evictionAttempts int
}

// NewController 创建初值时钟为 0 的控制器。
func NewController() *Controller { return &Controller{now: 0} }

// Submit 尝试接纳一个新作业。
func (c *Controller) Submit(id string, now, C, d, M, v int64) SubmitResult {
	if !validParams(id, now, C, d, M, v) {
		return SubmitResult{Accepted: false, Reason: RejectInvalidArgs}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.sorts = 0
	c.feasibilityScans = 0
	c.evictionAttempts = 0

	if now < c.now {
		return SubmitResult{Accepted: false, Reason: RejectClockBack}
	}

	// 被拒绝的操作不得落盘任何副作用（含推进结算），故先做完整快照。
	snap := c.snapshot()

	c.advanceLocked(now)

	if c.indexOf(id) >= 0 {
		c.restore(snap)
		return SubmitResult{Accepted: false, Reason: RejectDuplicateID}
	}

	if now+C > d+M {
		c.restore(snap)
		return SubmitResult{Accepted: false, Reason: RejectInfeasible}
	}

	incoming := &job{
		id:        id,
		remain:    C,
		deadline:  d,
		tolerance: M,
		value:     v,
	}
	c.insertSorted(incoming)

	// 首次可行性判定。
	if c.feasibleLocked() {
		return SubmitResult{Accepted: true, Reason: Accepted}
	}

	// 过载：反复驱逐价值密度最小且尚未开始者，每驱逐一个重判一次。
	var evictedNow []string
	for {
		c.evictionAttempts++
		victim := c.pickEvictionVictimLocked()
		if victim == nil {
			c.restore(snap)
			return SubmitResult{Accepted: false, Reason: RejectOverload}
		}
		c.removeJob(victim)
		if victim == incoming {
			// 新作业自己被选中：此前驱逐的作业全部恢复。
			c.restore(snap)
			return SubmitResult{Accepted: false, Reason: RejectOverload}
		}
		evictedNow = append(evictedNow, victim.id)
		if c.feasibleLocked() {
			break
		}
	}

	c.evicted = append(c.evicted, evictedNow...)
	return SubmitResult{
		Accepted: true,
		Reason:   Accepted,
		Evicted:  append([]string(nil), evictedNow...),
	}
}

// Advance 仅推进时钟并结算期间完成的作业。
func (c *Controller) Advance(now int64) Result {
	if now < 0 || now > 1_000_000_000_000_000 {
		return Result{OK: false, Reason: RejectInvalidArgs}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.now {
		return Result{OK: false, Reason: RejectClockBack}
	}

	c.advanceLocked(now)
	return Result{OK: true, Reason: Accepted}
}

// Pending 按 EDF 序返回待处理作业视图。
func (c *Controller) Pending() []PendingItem {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]PendingItem, len(c.pending))
	for i, j := range c.pending {
		out[i] = PendingItem{ID: j.id, Remain: j.remain, Started: j.started}
	}
	return out
}

// Value 返回累计价值。
func (c *Controller) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Evicted 按驱逐先后返回被驱逐编号。
func (c *Controller) Evicted() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.evicted...)
}

// Now 返回当前时钟。
func (c *Controller) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// lastSubmitCounters 暴露最近一次 Submit 的非导出计数器，供测试断言：
// sorts 为排序次数，scans 为可行性判定（扫描）次数，attempts 为驱逐尝试次数。
func (c *Controller) lastSubmitCounters() (sorts, scans, attempts int) {
	return c.sorts, c.feasibilityScans, c.evictionAttempts
}

func validParams(id string, now, C, d, M, v int64) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return false
	}
	if C < 1 || C > 1_000_000 {
		return false
	}
	if d < 0 || d > 1_000_000_000_000_000 {
		return false
	}
	if M < 0 || M > 1_000_000 {
		return false
	}
	if v < 1 || v > 1_000_000 {
		return false
	}
	return true
}

// advanceLocked 把时钟推进到 to：pending 按 EDF 序连续运行，
// 完成时刻 f<=to 的作业按 f 结算价值；跨越 to 的队首作业实际运行 to-t 个单位，
// 标记为已开始，时钟停在 to。任何时刻可行不变式保证完成者延迟不超过容忍值。
func (c *Controller) advanceLocked(to int64) {
	t := c.now
	for len(c.pending) > 0 {
		head := c.pending[0]
		f := t + head.remain
		if f <= to {
			c.settleLocked(head, f)
			c.pending = c.pending[1:]
			t = f
			continue
		}
		// to==t 时没有任何时间流逝（常见于 now 等于当前时钟的 Submit），
		// 队首未获得执行，不能标记已开始，也不能改变余量。
		if to > t {
			head.remain = f - to
			head.started = true
		}
		t = to
		break
	}
	if t < to {
		t = to
	}
	c.now = t
}

// settleLocked 按完成时刻 f 结算延迟折价价值：
// floor(v*(M+1-max(0,f-d))/(M+1))，准时（f<=d）为 v。
func (c *Controller) settleLocked(j *job, f int64) {
	lateness := f - j.deadline
	if lateness < 0 {
		lateness = 0
	}
	denom := j.tolerance + 1
	c.value += j.value * (denom - lateness) / denom
}

// feasibleLocked 从当前时钟起按 EDF 序恰好扫描一遍待处理作业，
// 判定每个完成时刻 f 是否满足 f-d<=M（恰等通过）。无论结果如何都完整扫描。
func (c *Controller) feasibleLocked() bool {
	c.feasibilityScans++
	t := c.now
	feasible := true
	for _, j := range c.pending {
		t += j.remain
		if t-j.deadline > j.tolerance {
			feasible = false
		}
	}
	return feasible
}

// pickEvictionVictimLocked 在未开始作业中选择价值密度最小者：
// 以 v1*r2 与 v2*r1 交叉相乘比较 v/remain（int64 下乘积不超过 1e12，不溢出），
// 密度并列时取编号字节序大者。
func (c *Controller) pickEvictionVictimLocked() *job {
	var victim *job
	for _, j := range c.pending {
		if j.started {
			continue
		}
		if victim == nil || lessDensity(j, victim) {
			victim = j
		}
	}
	return victim
}

// lessDensity 报告 a 是否应先于 b 被驱逐：密度更小，或密度相同而编号字节序更大。
func lessDensity(a, b *job) bool {
	lhs := a.value * b.remain
	rhs := b.value * a.remain
	if lhs != rhs {
		return lhs < rhs
	}
	return a.id > b.id
}

func (c *Controller) insertSorted(j *job) {
	pos := sort.Search(len(c.pending), func(i int) bool {
		return c.pending[i].deadline > j.deadline ||
			(c.pending[i].deadline == j.deadline && c.pending[i].id >= j.id)
	})
	c.pending = append(c.pending, nil)
	copy(c.pending[pos+1:], c.pending[pos:])
	c.pending[pos] = j
}

func (c *Controller) removeJob(target *job) {
	for i, j := range c.pending {
		if j == target {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			return
		}
	}
}

func (c *Controller) indexOf(id string) int {
	for i, j := range c.pending {
		if j.id == id {
			return i
		}
	}
	return -1
}

type snapshot struct {
	now     int64
	value   int64
	pending []*job
	evicted []string
}

func (c *Controller) snapshot() snapshot {
	pending := make([]*job, len(c.pending))
	for i, j := range c.pending {
		cp := *j
		pending[i] = &cp
	}
	return snapshot{
		now:     c.now,
		value:   c.value,
		pending: pending,
		evicted: append([]string(nil), c.evicted...),
	}
}

func (c *Controller) restore(s snapshot) {
	c.now = s.now
	c.value = s.value
	// 快照中的作业可能已被本次推进修改（remain/started），恢复时再次深拷贝，
	// 使后续操作不会在快照对象上继续就地改写。
	pending := make([]*job, len(s.pending))
	for i, j := range s.pending {
		cp := *j
		pending[i] = &cp
	}
	c.pending = pending
	c.evicted = s.evicted
}
