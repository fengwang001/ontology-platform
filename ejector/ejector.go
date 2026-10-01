// Package ejector 实现离群点摘除器：按连续失败次数或滑动结果窗口失败率
// 摘除主机，摘除时长随累计摘除次数线性加长，并受最大摘除比例上限约束。
package ejector

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	// maxTime 是合法时刻的上界（含）。
	maxTime = int64(1_000_000_000_000_000) // 1e15
	// maxDuration 是基础摘除时长与摘除时长上限的上界（含）。
	maxDuration = int64(1_000_000_000) // 1e9
)

var (
	// ErrInvalidConfig 表示构造参数整体非法。
	ErrInvalidConfig = errors.New("ejector: invalid config")
	// ErrHostOutOfRange 表示主机编号越界。
	ErrHostOutOfRange = errors.New("ejector: host out of range")
	// ErrInvalidTime 表示时刻非法（小于 0 或大于 1e15）。
	ErrInvalidTime = errors.New("ejector: invalid time")
	// ErrClockRegression 表示时刻小于已接受上报见过的最大时刻。
	ErrClockRegression = errors.New("ejector: clock regression")
)

// Outcome 是一次被接受的上报的处理结果。
type Outcome int

const (
	// OutcomeRecorded 上报已记录，未触发摘除。
	OutcomeRecorded Outcome = iota
	// OutcomeIgnored 主机此刻被摘除，上报被忽略，状态不变。
	OutcomeIgnored
	// OutcomeEjected 触发条件满足且比例上限允许，主机被摘除。
	OutcomeEjected
	// OutcomeCapped 触发条件满足但被最大摘除比例挡下。
	OutcomeCapped
)

// String 返回结果的可读描述。
func (o Outcome) String() string {
	switch o {
	case OutcomeRecorded:
		return "recorded"
	case OutcomeIgnored:
		return "ignored"
	case OutcomeEjected:
		return "ejected"
	case OutcomeCapped:
		return "capped"
	default:
		return "unknown"
	}
}

// Config 是摘除器的构造参数，所有时长与时刻均为 int64。
type Config struct {
	N   int64 // 主机数，编号 0 到 N-1，>= 1
	K   int64 // 连续失败阈值，>= 1
	B   int64 // 基础摘除时长，1..1e9
	Cap int64 // 摘除时长上限，B..1e9
	P   int64 // 最大摘除百分比，0..100
	Wn  int64 // 结果窗口长度，1..64
	Q   int64 // 窗口失败率阈值（百分数），1..100
}

// Event 是 ReportBatch 的单个上报事件。
type Event struct {
	Host int
	Ok   bool
	Now  int64
}

// hostState 是单台主机的内部状态。
type hostState struct {
	c   int64  // 连续失败数
	e   int64  // 累计摘除次数
	u   int64  // 摘除截止时刻，u > now 表示被摘除
	win []bool // 最近至多 Wn 次已记录上报，true 为成功，最旧在前
}

// Ejector 是离群点摘除器，所有方法可并发安全调用。
type Ejector struct {
	cfg    Config
	mu     sync.Mutex
	hosts  []hostState
	maxNow int64 // 已接受的 Report 与 ReportBatch 见过的最大 now
}

// NewEjector 校验配置并构造摘除器；配置非法时整体拒绝。
func NewEjector(cfg Config) (*Ejector, error) {
	if !validConfig(cfg) {
		return nil, ErrInvalidConfig
	}
	return &Ejector{cfg: cfg, hosts: make([]hostState, cfg.N)}, nil
}

func validConfig(cfg Config) bool {
	if cfg.N < 1 || cfg.K < 1 || cfg.B < 1 || cfg.B > maxDuration {
		return false
	}
	if cfg.Cap < cfg.B || cfg.Cap > maxDuration {
		return false
	}
	if cfg.P < 0 || cfg.P > 100 {
		return false
	}
	if cfg.Wn < 1 || cfg.Wn > 64 {
		return false
	}
	if cfg.Q < 1 || cfg.Q > 100 {
		return false
	}
	return true
}

// checkHost 与 checkTime 按固定顺序返回第一个拒绝原因：
// 先主机编号越界，再时间非法，最后时钟回退。
func (e *Ejector) checkHost(host int) error {
	if host < 0 || int64(host) >= e.cfg.N {
		return ErrHostOutOfRange
	}
	return nil
}

func (e *Ejector) checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < e.maxNow {
		return ErrClockRegression
	}
	return nil
}

// ejectedCount 统计 now 时刻被摘除（u > now）的主机数。
func (e *Ejector) ejectedCount(now int64) int64 {
	var cnt int64
	for i := range e.hosts {
		if e.hosts[i].u > now {
			cnt++
		}
	}
	return cnt
}

// apply 按 Report 规则处理一条已通过校验的上报，调用方须持有锁。
func (e *Ejector) apply(host int, ok bool, now int64) Outcome {
	h := &e.hosts[host]
	if h.u > now {
		return OutcomeIgnored
	}
	if ok {
		h.c = 0
		if h.e > 0 {
			h.e--
		}
		h.win = appendWindow(h.win, true, e.cfg.Wn)
		return OutcomeRecorded
	}
	h.c++
	h.win = appendWindow(h.win, false, e.cfg.Wn)
	var fails int64
	for _, v := range h.win {
		if !v {
			fails++
		}
	}
	windowFull := int64(len(h.win)) == e.cfg.Wn
	if h.c < e.cfg.K && !(windowFull && fails*100 >= e.cfg.Q*e.cfg.Wn) {
		return OutcomeRecorded
	}
	// 触发摘除，检查比例上限：(E+1)*100 <= P*N，整数比较。
	if (e.ejectedCount(now)+1)*100 > e.cfg.P*e.cfg.N {
		return OutcomeCapped
	}
	h.e++
	h.u = now + ejectDuration(e.cfg.B, e.cfg.Cap, h.e)
	h.c = 0
	h.win = nil
	return OutcomeEjected
}

// appendWindow 追加一次上报结果，超过 Wn 时丢弃最旧项。
func appendWindow(win []bool, ok bool, wn int64) []bool {
	win = append(win, ok)
	if int64(len(win)) > wn {
		win = win[1:]
	}
	return win
}

// ejectDuration 计算第 e 次摘除的时长 min(Cap, B*e)，避免溢出。
func ejectDuration(b, maxDur, e int64) int64 {
	if e > maxDur/b {
		return maxDur
	}
	if d := b * e; d < maxDur {
		return d
	}
	return maxDur
}

// Report 处理单次上报，返回处理结果或第一个拒绝原因。
// 被接受的上报（含已忽略与被上限挡下）会推进最大时刻。
func (e *Ejector) Report(host int, ok bool, now int64) (Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkHost(host); err != nil {
		return 0, err
	}
	if err := e.checkTime(now); err != nil {
		return 0, err
	}
	e.maxNow = now
	return e.apply(host, ok, now), nil
}

// ReportBatch 原子处理一批乱序到达的上报，返回值按输入顺序排列。
// 预检任一不通过则整批不改任何状态；通过后按 now 升序稳定排序逐个处理。
func (e *Ejector) ReportBatch(events []Event) ([]Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(events) == 0 {
		return []Outcome{}, nil
	}
	for i, ev := range events {
		if err := e.checkHost(ev.Host); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}
		if ev.Now < 0 || ev.Now > maxTime {
			return nil, fmt.Errorf("event %d: %w", i, ErrInvalidTime)
		}
	}
	minNow, maxNow := events[0].Now, events[0].Now
	for _, ev := range events[1:] {
		if ev.Now < minNow {
			minNow = ev.Now
		}
		if ev.Now > maxNow {
			maxNow = ev.Now
		}
	}
	if minNow < e.maxNow {
		return nil, ErrClockRegression
	}
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return events[order[a]].Now < events[order[b]].Now
	})
	outcomes := make([]Outcome, len(events))
	for _, idx := range order {
		ev := events[idx]
		outcomes[idx] = e.apply(ev.Host, ev.Ok, ev.Now)
	}
	e.maxNow = maxNow
	return outcomes, nil
}

// Ejected 返回主机在 now 时刻是否被摘除，不推进最大时刻。
func (e *Ejector) Ejected(host int, now int64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkHost(host); err != nil {
		return false, err
	}
	if err := e.checkTime(now); err != nil {
		return false, err
	}
	return e.hosts[host].u > now, nil
}

// Healthy 返回 now 时刻未被摘除的主机编号升序列表，不推进最大时刻。
func (e *Ejector) Healthy(now int64) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkTime(now); err != nil {
		return nil, err
	}
	healthy := make([]int, 0, len(e.hosts))
	for i := range e.hosts {
		if e.hosts[i].u <= now {
			healthy = append(healthy, i)
		}
	}
	return healthy, nil
}
