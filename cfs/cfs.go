// Package cfs 实现多 CPU 的 CFS 带宽控制器（配额与周期节流）。
//
// 各 CPU 在本地额度耗尽后按借用片 S 向全局运行时间池借用额度，
// 借不到即被节流；周期边界补满全局池并按节流先后补给被节流的 CPU；
// CPU 空闲时把多余的本地额度归还全局池。
package cfs

import (
	"errors"
	"sync"
)

// 参数取值边界。
const (
	maxQPSB = int64(1_000_000_000)         // Q、P、S、B 的上界 10^9
	maxNow  = int64(1_000_000_000_000_000) // now 的上界 10^15
	maxRun  = int64(1_000_000)             // Run 时长 d 的上界 10^6
	maxCPU  = 64                           // CPU 数上界
)

// CPUState 表示单个 CPU 的运行状态。
type CPUState int

const (
	// Idle 空闲。
	Idle CPUState = iota
	// Running 运行中。
	Running
	// Throttled 被节流。
	Throttled
)

func (s CPUState) String() string {
	switch s {
	case Idle:
		return "idle"
	case Running:
		return "running"
	case Throttled:
		return "throttled"
	}
	return "unknown"
}

// 可区分的错误，调用方可用 errors.Is 判定。
var (
	// ErrInvalidConfig 构造参数越界，整体拒绝。
	ErrInvalidConfig = errors.New("cfs: invalid configuration")
	// ErrCPUOutOfRange cpu 编号越界。
	ErrCPUOutOfRange = errors.New("cfs: cpu index out of range")
	// ErrInvalidParam now 不在 [0, 10^15]，或 Run 的 d 不在 [1, 10^6]。
	ErrInvalidParam = errors.New("cfs: invalid parameter")
	// ErrTimeRegression now 小于上一次被接受操作的时刻。
	ErrTimeRegression = errors.New("cfs: time regression")
	// ErrNotIdle Wake 时 CPU 不是空闲。
	ErrNotIdle = errors.New("cfs: cpu not idle")
	// ErrNotRunning Run 或 Idle 时 CPU 为空闲。
	ErrNotRunning = errors.New("cfs: cpu not running")
	// ErrThrottled Run 或 Idle 时 CPU 被节流。
	ErrThrottled = errors.New("cfs: cpu throttled")
)

// cpu 是单个 CPU 的可变状态。
type cpu struct {
	state CPUState
	l     int64 // 本地余量，可为负
	since int64 // 被节流起点
}

// config 为构造后不可变的配置。
type config struct {
	q int64 // 每周期配额
	p int64 // 周期
	s int64 // 借用片
	b int64 // 突发上限（允许结转的最大额外余量）
}

// poolCap 全局池上限 Q+B。
func (c *config) poolCap() int64 { return c.q + c.b }

// state 为控制器的全部可变状态，操作时先fork副本推演，接受后才提交。
type state struct {
	g             int64 // 全局池余量
	cpus          []cpu
	queue         []int // 节流队列，先进先出
	periods       int64 // 已处理边界数
	nThrottled    int64 // 节流事件数
	throttledTime int64 // 累计节流时长
	lastNow       int64 // 上一次被接受操作的时刻
	boundaryIters int64 // 逐个处理边界的次数（复杂度计数器）
}

// fork 深拷贝可变状态。
func (st *state) fork() state {
	n := *st
	n.cpus = append([]cpu(nil), st.cpus...)
	n.queue = append([]int(nil), st.queue...)
	return n
}

// Controller 是多 CPU 的 CFS 带宽控制器，所有方法可并发调用。
type Controller struct {
	mu  sync.Mutex
	cfg config
	st  state
}

// New 构造控制器。quota、period、slice 须在 [1, 10^9]，burst 须在 [0, 10^9]，
// cpus 须在 [1, 64]，任一越界则整体拒绝并返回 ErrInvalidConfig。
func New(quota, period, slice, burst int64, cpus int) (*Controller, error) {
	if quota < 1 || quota > maxQPSB ||
		period < 1 || period > maxQPSB ||
		slice < 1 || slice > maxQPSB ||
		burst < 0 || burst > maxQPSB ||
		cpus < 1 || cpus > maxCPU {
		return nil, ErrInvalidConfig
	}
	return &Controller{
		cfg: config{q: quota, p: period, s: slice, b: burst},
		st:  state{g: quota, cpus: make([]cpu, cpus)},
	}, nil
}

// advance 处理所有满足 k*P <= now 的尚未处理的边界。
// 只有节流队列非空的边界才逐个处理；队列空了之后剩余的边界以算术一次完成。
func (st *state) advance(cfg *config, now int64) {
	k := now/cfg.p - st.periods
	for k > 0 && len(st.queue) > 0 {
		st.periods++
		st.boundaryIters++
		st.g = min(cfg.poolCap(), st.g+cfg.q)
		st.replenish(st.periods * cfg.p)
		k--
	}
	if k > 0 {
		st.periods += k
		if pc := cfg.poolCap(); st.g < pc {
			// 防溢出：k 可达 10^15，k*Q 可达 10^24，超出 int64。
			if k > (pc-st.g)/cfg.q {
				st.g = pc
			} else {
				st.g += k * cfg.q
			}
		}
	}
}

// replenish 在边界 b 上按节流队列先后逐个补给被节流的 CPU。
func (st *state) replenish(b int64) {
	q := st.queue
	i := 0
	for i < len(q) && st.g > 0 {
		cp := &st.cpus[q[i]]
		need := 1 - cp.l // l <= 0，故 need >= 1
		take := min(need, st.g)
		cp.l += take
		st.g -= take
		if cp.l > 0 {
			cp.state = Running
			st.throttledTime += b - cp.since
			i++ // 出队
		}
		// take < need 时 G 已为 0，队列中其余 CPU 保持节流。
	}
	st.queue = q[i:]
}

// checkCPU 校验 cpu 编号。
func (c *Controller) checkCPU(cpuID int) error {
	if cpuID < 0 || cpuID >= len(c.st.cpus) {
		return ErrCPUOutOfRange
	}
	return nil
}

// checkNow 校验 now 取值范围。
func checkNow(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	return nil
}

// checkRegression 校验时间未回退。
func (c *Controller) checkRegression(now int64) error {
	if now < c.st.lastNow {
		return ErrTimeRegression
	}
	return nil
}

// Wake 空闲变运行（l 不变）。
func (c *Controller) Wake(now int64, cpuID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkCPU(cpuID); err != nil {
		return err
	}
	if err := checkNow(now); err != nil {
		return err
	}
	if err := c.checkRegression(now); err != nil {
		return err
	}
	st := c.st.fork()
	st.advance(&c.cfg, now)
	cp := &st.cpus[cpuID]
	if cp.state != Idle {
		return ErrNotIdle
	}
	cp.state = Running
	st.lastNow = now
	c.st = st
	return nil
}

// Run 表示该 CPU 刚连续运行了 d 个时间单位，无论是否越界都全额消耗。
func (c *Controller) Run(now int64, cpuID int, d int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkCPU(cpuID); err != nil {
		return err
	}
	if err := checkNow(now); err != nil {
		return err
	}
	if d < 1 || d > maxRun {
		return ErrInvalidParam
	}
	if err := c.checkRegression(now); err != nil {
		return err
	}
	st := c.st.fork()
	st.advance(&c.cfg, now)
	cp := &st.cpus[cpuID]
	switch cp.state {
	case Idle:
		return ErrNotRunning
	case Throttled:
		return ErrThrottled
	}
	cp.l -= d
	if cp.l <= 0 {
		want := c.cfg.s - cp.l
		take := min(want, st.g)
		cp.l += take
		st.g -= take
		if cp.l <= 0 {
			cp.state = Throttled
			cp.since = now
			st.queue = append(st.queue, cpuID)
			st.nThrottled++
		}
	}
	st.lastNow = now
	c.st = st
	return nil
}

// Idle 运行变空闲；l 大于 1 时把多余额度归还全局池（受 Q+B 封顶）。
func (c *Controller) Idle(now int64, cpuID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkCPU(cpuID); err != nil {
		return err
	}
	if err := checkNow(now); err != nil {
		return err
	}
	if err := c.checkRegression(now); err != nil {
		return err
	}
	st := c.st.fork()
	st.advance(&c.cfg, now)
	cp := &st.cpus[cpuID]
	switch cp.state {
	case Idle:
		return ErrNotRunning
	case Throttled:
		return ErrThrottled
	}
	cp.state = Idle
	if cp.l > 1 {
		st.g = min(c.cfg.poolCap(), st.g+cp.l-1)
		cp.l = 1
	}
	st.lastNow = now
	c.st = st
	return nil
}

// Stats 返回已处理边界数、节流事件数与累计节流时长。
func (c *Controller) Stats() (periods, nThrottled, throttledTime int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st.periods, c.st.nThrottled, c.st.throttledTime
}

// State 返回指定 CPU 的状态、本地余量与被节流起点。
// cpu 编号越界时返回零值。
func (c *Controller) State(cpuID int) (CPUState, int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cpuID < 0 || cpuID >= len(c.st.cpus) {
		return Idle, 0, 0
	}
	cp := c.st.cpus[cpuID]
	return cp.state, cp.l, cp.since
}

// Pool 返回全局池余量 G。
func (c *Controller) Pool() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st.g
}

// Queue 返回节流队列（先进先出）的副本。
func (c *Controller) Queue() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.st.queue...)
}
