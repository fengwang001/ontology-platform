// Package throttle 实现按欠额的确定性减载与候选改派。
// 每条消息的转发目标、减载位置与拒绝原因都可由轨迹精确复现。
package throttle

import (
	"fmt"

	"ontology/classify"
	"ontology/ocstate"
)

// 错误哨兵（与 ocstate 共享，便于 errors.Is 统一区分）。
var (
	ErrInvalidParam   = ocstate.ErrInvalidParam
	ErrClockBack      = ocstate.ErrClockBack
	ErrServerNotExist = ocstate.ErrServerNotExist
	ErrServerExists   = ocstate.ErrServerExists
	ErrStaleReport    = ocstate.ErrStaleReport
	ErrNoInflight     = ocstate.ErrNoInflight
)

// 参数合法范围。
const (
	MinThreshold  = 100
	MaxCap        = 1_000_000
	MinWindow     = 1
	MaxWindow     = 100_000
	MaxCandidates = 8
)

// Action 为逐台轨迹中一台候选上的判定结果。
type Action int

const (
	ActionForwarded Action = iota
	ActionDropped
	ActionBusy
)

func (a Action) String() string {
	switch a {
	case ActionForwarded:
		return "Forwarded"
	case ActionDropped:
		return "Dropped"
	case ActionBusy:
		return "Busy"
	}
	return "Unknown"
}

// Step 记录一台候选服务器上的判定。
type Step struct {
	Server int
	Action Action
}

// Outcome 为 Route 的最终结果。
type Outcome int

const (
	OutcomeForwarded Outcome = iota
	OutcomeRejected
)

// Reason 为拒绝原因（仅 OutcomeRejected 时有效）。
type Reason int

const (
	ReasonNone Reason = iota
	// ReasonOverload 轨迹中出现过减载。
	ReasonOverload
	// ReasonBusy 轨迹全为 Busy。
	ReasonBusy
)

func (r Reason) String() string {
	switch r {
	case ReasonOverload:
		return "Overload"
	case ReasonBusy:
		return "Busy"
	}
	return "None"
}

// Result 为 Route 的返回：结果、目标服务器与逐台轨迹。
type Result struct {
	Outcome Outcome
	Server  int // 仅 OutcomeForwarded 时有效
	Reason  Reason
	Trail   []Step
}

// Controller 为下游过载控制器。
type Controller struct {
	store  *ocstate.Store
	thLow  int64
	thNorm int64
	dcap   int64
	w      int64

	touched int // 非导出计数器：一次 Route 触碰的服务器记录数
}

// New 创建控制器，要求 100<=thLow<=thNorm<=dcap<=1e6，1<=w<=1e5。
func New(thLow, thNorm, dcap, w int64) (*Controller, error) {
	if thLow < MinThreshold || thNorm < thLow || dcap < thNorm ||
		dcap > MaxCap || w < MinWindow || w > MaxWindow {
		return nil, fmt.Errorf("new(thLow=%d thNorm=%d dcap=%d w=%d): %w",
			thLow, thNorm, dcap, w, ErrInvalidParam)
	}
	return &Controller{
		store:  ocstate.NewStore(),
		thLow:  thLow,
		thNorm: thNorm,
		dcap:   dcap,
		w:      w,
	}, nil
}

// AddServer 登记下游服务器。
func (c *Controller) AddServer(id int) error { return c.store.AddServer(id) }

// Report 接受某服务器的过载通告。
func (c *Controller) Report(server int, seq, percent, validity, now int64) error {
	return c.store.Report(server, seq, percent, validity, now)
}

// Done 使某服务器在途数减 1。
func (c *Controller) Done(server int, now int64) error {
	return c.store.Done(server, now)
}

// Inspect 返回服务器状态快照，供测试核对。
func (c *Controller) Inspect(server int) ocstate.Snapshot {
	return c.store.Inspect(server)
}

// Route 按候选顺序逐台尝试转发一条消息。
// 拒绝（全部候选失败）是正常结果而非错误；错误仅用于参数非法、
// 时钟回退与服务器不存在，且出错时不改任何状态与时钟。
func (c *Controller) Route(msg classify.Message, candidates []int, now int64) (Result, error) {
	class, err := classify.Classify(msg)
	if err != nil {
		return Result{}, fmt.Errorf("route method %q: %w", msg.Method, ErrInvalidParam)
	}
	if len(candidates) < 1 || len(candidates) > MaxCandidates {
		return Result{}, fmt.Errorf("route %d candidates: %w", len(candidates), ErrInvalidParam)
	}
	seen := make(map[int]bool, len(candidates))
	for _, id := range candidates {
		if id < ocstate.MinServerID || id > ocstate.MaxServerID {
			return Result{}, fmt.Errorf("route candidate %d: %w", id, ErrInvalidParam)
		}
		if seen[id] {
			return Result{}, fmt.Errorf("route duplicate candidate %d: %w", id, ErrInvalidParam)
		}
		seen[id] = true
	}

	c.store.Lock()
	defer c.store.Unlock()

	if err := c.store.CheckNow(now); err != nil {
		return Result{}, err
	}
	for _, id := range candidates {
		if !c.store.Exists(id) {
			return Result{}, fmt.Errorf("route candidate %d: %w", id, ErrServerNotExist)
		}
	}

	c.touched = 0
	trail := make([]Step, 0, len(candidates))
	dropped := false
	for _, id := range candidates {
		srv := c.store.Server(id)
		c.touched++
		if srv.Inflight() >= c.w {
			trail = append(trail, Step{Server: id, Action: ActionBusy})
			continue
		}
		srv.Settle(now)
		srv.Arrive(c.dcap)
		forward := false
		switch class {
		case classify.Exempt:
			forward = true
		case classify.Low:
			if srv.D() >= c.thLow {
				srv.Drop()
				trail = append(trail, Step{Server: id, Action: ActionDropped})
				dropped = true
			} else {
				forward = true
			}
		case classify.Normal:
			if srv.D() >= c.thNorm {
				srv.Drop()
				trail = append(trail, Step{Server: id, Action: ActionDropped})
				dropped = true
			} else {
				forward = true
			}
		}
		if forward {
			srv.Forward()
			trail = append(trail, Step{Server: id, Action: ActionForwarded})
			c.store.Advance(now)
			return Result{Outcome: OutcomeForwarded, Server: id, Trail: trail}, nil
		}
	}

	c.store.Advance(now)
	reason := ReasonBusy
	if dropped {
		reason = ReasonOverload
	}
	return Result{Outcome: OutcomeRejected, Reason: reason, Trail: trail}, nil
}
