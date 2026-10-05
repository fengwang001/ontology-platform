// Package throttle 实现信令代理的下游过载控制器：
// 按欠额确定性减载，被减载的消息改派下一候选，全部失败才拒绝。
// 所有操作由一把互斥锁串行化，并发调用等价于某个串行顺序。
package throttle

import (
	"errors"
	"fmt"
	"sync"

	"ontology/classify"
	"ontology/ocstate"
)

var (
	// ErrInvalidParam 参数非法（候选重复、数量越界、id/now/percent/validity 越界等）。
	ErrInvalidParam = errors.New("throttle: invalid parameter")
	// ErrClockRegression 时钟回退：now 小于已接受的最大 now。
	ErrClockRegression = errors.New("throttle: clock regression")
	// ErrServerNotFound 任一候选或目标服务器未登记。
	ErrServerNotFound = errors.New("throttle: server not registered")
	// ErrAlreadyExists AddServer 重复登记。
	ErrAlreadyExists = errors.New("throttle: server already exists")
)

const (
	maxNow      = int64(1_000_000_000_000)
	maxServerID = int64(1_000_000)
	maxCap      = int64(1_000_000)
	maxWindow   = int64(100_000)
	maxValidity = int64(1_000_000_000)
	maxCands    = 8
)

// Outcome 为 Route 的最终结果。
type Outcome int

const (
	Forwarded        Outcome = iota // 已转发到 Target
	RejectedOverload                // 拒绝：轨迹中出现过减载
	RejectedBusy                    // 拒绝：轨迹全为 Busy
)

func (o Outcome) String() string {
	switch o {
	case Forwarded:
		return "Forwarded"
	case RejectedOverload:
		return "Rejected(overload)"
	default:
		return "Rejected(busy)"
	}
}

// StepOutcome 为单台候选上的尝试结果。
type StepOutcome int

const (
	StepBusy      StepOutcome = iota // 在途已满，未计到达、未改 D
	StepDropped                      // 到达后被减载，D 已扣 100
	StepForwarded                    // 已转发，在途数加 1
)

func (s StepOutcome) String() string {
	switch s {
	case StepBusy:
		return "Busy"
	case StepDropped:
		return "Dropped"
	default:
		return "Forwarded"
	}
}

// Step 为逐台轨迹中的一项。
type Step struct {
	Server  int64
	Outcome StepOutcome
}

// Result 为 Route 的返回：结果、目标服务器、逐台轨迹。
// 拒绝是结果而非错误，其对 D 的改动照常生效、时钟照常推进。
type Result struct {
	Outcome Outcome
	Target  int64 // 仅 Outcome 为 Forwarded 时有效
	Trace   []Step
}

// Controller 为下游过载控制器。
type Controller struct {
	mu        sync.Mutex
	thetaLow  int64
	thetaNorm int64
	dCap      int64
	window    int64
	servers   map[int64]*ocstate.Server
	maxNow    int64
	touched   int // 非导出计数器：一次 Route 触碰的服务器记录数
}

// New 创建控制器，要求 100<=thetaLow<=thetaNorm<=dCap<=1e6 且 1<=window<=1e5。
func New(thetaLow, thetaNorm, dCap, window int64) (*Controller, error) {
	if thetaLow < 100 || thetaLow > thetaNorm || thetaNorm > dCap || dCap > maxCap {
		return nil, fmt.Errorf("%w: thresholds (%d,%d,%d)", ErrInvalidParam, thetaLow, thetaNorm, dCap)
	}
	if window < 1 || window > maxWindow {
		return nil, fmt.Errorf("%w: window %d", ErrInvalidParam, window)
	}
	return &Controller{
		thetaLow:  thetaLow,
		thetaNorm: thetaNorm,
		dCap:      dCap,
		window:    window,
		servers:   make(map[int64]*ocstate.Server),
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// AddServer 登记服务器，id 为 1 到 1e6，重复报 ErrAlreadyExists。
func (c *Controller) AddServer(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 1 || id > maxServerID {
		return fmt.Errorf("%w: server id %d", ErrInvalidParam, id)
	}
	if _, ok := c.servers[id]; ok {
		return ErrAlreadyExists
	}
	c.servers[id] = ocstate.NewServer(id)
	return nil
}

// Report 接受过载通告。校验次序：参数非法 > 时钟回退 > 服务器不存在 > 通告过期。
func (c *Controller) Report(server, seq, percent, validity, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if percent < 0 || percent > 100 || validity < 0 || validity > maxValidity || !validNow(now) {
		return fmt.Errorf("%w: report(server=%d seq=%d p=%d v=%d now=%d)",
			ErrInvalidParam, server, seq, percent, validity, now)
	}
	if now < c.maxNow {
		return ErrClockRegression
	}
	s, ok := c.servers[server]
	if !ok {
		return fmt.Errorf("%w: %d", ErrServerNotFound, server)
	}
	if err := s.Report(seq, percent, validity, now); err != nil {
		return err
	}
	c.maxNow = now
	return nil
}

// Done 将该服务器在途数减 1。校验次序：参数非法 > 时钟回退 > 服务器不存在 > 无在途。
func (c *Controller) Done(server, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validNow(now) {
		return fmt.Errorf("%w: done(server=%d now=%d)", ErrInvalidParam, server, now)
	}
	if now < c.maxNow {
		return ErrClockRegression
	}
	s, ok := c.servers[server]
	if !ok {
		return fmt.Errorf("%w: %d", ErrServerNotFound, server)
	}
	if err := s.Done(); err != nil {
		return err
	}
	c.maxNow = now
	return nil
}

// Route 按序逐台尝试候选服务器。校验次序：
// 参数非法（未知方法、候选数量越界、候选重复）> 时钟回退 > 服务器不存在。
// 校验失败返回 error 且不改任何状态与时钟；路由层面的拒绝是 Result 而非 error。
func (c *Controller) Route(msg classify.Message, candidates []int64, now int64) (Result, error) {
	class, cerr := classify.Classify(msg)

	c.mu.Lock()
	defer c.mu.Unlock()
	if cerr != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidParam, cerr)
	}
	if !validNow(now) || len(candidates) < 1 || len(candidates) > maxCands {
		return Result{}, fmt.Errorf("%w: route(candidates=%d now=%d)", ErrInvalidParam, len(candidates), now)
	}
	seen := make(map[int64]struct{}, len(candidates))
	for _, id := range candidates {
		if _, dup := seen[id]; dup {
			return Result{}, fmt.Errorf("%w: duplicate candidate %d", ErrInvalidParam, id)
		}
		seen[id] = struct{}{}
	}
	if now < c.maxNow {
		return Result{}, ErrClockRegression
	}
	for _, id := range candidates {
		if _, ok := c.servers[id]; !ok {
			return Result{}, fmt.Errorf("%w: %d", ErrServerNotFound, id)
		}
	}

	c.touched = 0
	res := c.route(class, candidates, now)
	c.maxNow = now
	return res, nil
}

// route 为路由主循环，调用方已持锁并完成全部校验。
func (c *Controller) route(class classify.Class, candidates []int64, now int64) Result {
	trace := make([]Step, 0, len(candidates))
	dropped := false
	for _, id := range candidates {
		s := c.servers[id]
		c.touched++
		if s.InFlight >= c.window {
			trace = append(trace, Step{Server: id, Outcome: StepBusy})
			continue
		}
		s.Arrivals++
		s.D = s.EffectiveD(now) + s.EffectiveP(now)
		if s.D > c.dCap {
			s.D = c.dCap
		}
		shed := class == classify.ClassLow && s.D >= c.thetaLow ||
			class == classify.ClassNormal && s.D >= c.thetaNorm
		if shed {
			s.D -= 100
			s.Dropped++
			dropped = true
			trace = append(trace, Step{Server: id, Outcome: StepDropped})
			continue
		}
		s.InFlight++
		s.Forwarded++
		trace = append(trace, Step{Server: id, Outcome: StepForwarded})
		return Result{Outcome: Forwarded, Target: id, Trace: trace}
	}
	if dropped {
		return Result{Outcome: RejectedOverload, Trace: trace}
	}
	return Result{Outcome: RejectedBusy, Trace: trace}
}
