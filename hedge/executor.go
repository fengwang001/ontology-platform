package hedge

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// Response 副本应答。
type Response struct {
	Value any
	Err   error
}

// Transport 向副本发请求的传输层。
// deliver 必须恰好被调用一次，且允许在 cancel 之后迟到（执行器会丢弃）；
// deliver 与 cancel 都不得同步回调进执行器（应异步投递），cancel 必须非阻塞。
type Transport interface {
	Start(replica string, deliver func(Response)) (cancel func())
}

// Spec 单次调用的输入。
type Spec struct {
	Replicas    []string      // 有序副本列表
	HedgeDelay  time.Duration // 对冲延迟
	MaxInFlight int           // 最大在途数
	Deadline    time.Time     // 截止时间（基于注入时钟）
	Idempotent  bool          // 非幂等请求只发首个副本，不对冲不重试
}

// OutcomeKind 调用结局种类，每次调用恰好一个结局。
type OutcomeKind int

const (
	Success   OutcomeKind = iota // 某个副本成功，结果被采用
	AllFailed                    // 全部已发出请求失败
	Timeout                      // 到达截止时间
	Rejected                     // 参数非法，未发出任何请求
)

func (k OutcomeKind) String() string {
	switch k {
	case Success:
		return "success"
	case AllFailed:
		return "all-failed"
	case Timeout:
		return "timeout"
	case Rejected:
		return "rejected"
	}
	return "unknown"
}

// AttemptError 单个副本的错误，全部失败时按发出顺序收集。
type AttemptError struct {
	Replica string
	Err     error
}

// Outcome 调用结局。
type Outcome struct {
	Kind    OutcomeKind
	Value   any            // Success 时被采用的值
	Replica string         // Success 时胜出的副本
	Errors  []AttemptError // AllFailed 时按发出顺序排列
	Reason  error          // Rejected 的可区分原因 / Timeout / AllFailed 哨兵错误
	Hedges  int            // 本次调用发出的对冲数
	Retries int            // 本次调用发出的重试数
}

var (
	ErrNoReplicas             = errors.New("hedge: replica list is empty")
	ErrDuplicateReplica       = errors.New("hedge: duplicate replica")
	ErrNonPositiveHedgeDelay  = errors.New("hedge: hedge delay must be positive")
	ErrNonPositiveMaxInFlight = errors.New("hedge: max in-flight must be positive")
	ErrDeadlinePassed         = errors.New("hedge: deadline already passed")
	ErrTimeout                = errors.New("hedge: deadline exceeded")
	ErrAllFailed              = errors.New("hedge: all replicas failed")
)

// Executor 带全局预算的对冲请求执行器，可并发使用。
type Executor struct {
	clock     Clock
	budget    *Budget
	transport Transport
	logger    *log.Logger

	mu  sync.Mutex
	seq int
}

func NewExecutor(clock Clock, budget *Budget, transport Transport, logger *log.Logger) *Executor {
	return &Executor{clock: clock, budget: budget, transport: transport, logger: logger}
}

// Execute 同步执行一次调用，阻塞直到出现唯一结局。
func (e *Executor) Execute(spec Spec) Outcome {
	return e.Start(spec).Wait()
}

// Start 校验并启动一次调用，立即返回句柄；非法参数返回已含 Rejected 结局的句柄。
func (e *Executor) Start(spec Spec) *CallHandle {
	h := &CallHandle{done: make(chan struct{})}
	if err := validateSpec(spec, e.clock.Now()); err != nil {
		e.logf("call rejected: reason=%v", err)
		h.outcome = Outcome{Kind: Rejected, Reason: err}
		close(h.done)
		return h
	}
	e.budget.accept()
	e.mu.Lock()
	e.seq++
	id := e.seq
	e.mu.Unlock()
	c := newCall(e, id, spec, h)
	c.start()
	return h
}

// CallHandle 一次调用的句柄。
type CallHandle struct {
	done    chan struct{}
	outcome Outcome
}

// Wait 阻塞直到唯一结局出现并返回之。
func (h *CallHandle) Wait() Outcome {
	<-h.done
	return h.outcome
}

func (e *Executor) logf(format string, args ...any) {
	if e.logger != nil {
		e.logger.Printf(format, args...)
	}
}

func validateSpec(spec Spec, now time.Time) error {
	if len(spec.Replicas) == 0 {
		return ErrNoReplicas
	}
	seen := make(map[string]struct{}, len(spec.Replicas))
	for _, r := range spec.Replicas {
		if _, dup := seen[r]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateReplica, r)
		}
		seen[r] = struct{}{}
	}
	if spec.HedgeDelay <= 0 {
		return ErrNonPositiveHedgeDelay
	}
	if spec.MaxInFlight <= 0 {
		return ErrNonPositiveMaxInFlight
	}
	if !spec.Deadline.After(now) {
		return ErrDeadlinePassed
	}
	return nil
}

type sendKind int

const (
	initial sendKind = iota
	hedge
	retry
)

func (k sendKind) String() string {
	switch k {
	case initial:
		return "initial"
	case hedge:
		return "hedge"
	case retry:
		return "retry"
	}
	return "unknown"
}

type attempt struct {
	id      int
	replica string
	cancel  func()
	failed  bool
	err     error
}

// call 单次调用的状态机，所有事件处理都在 mu 下串行化。
type call struct {
	ex   *Executor
	id   int
	spec Spec
	h    *CallHandle

	mu            sync.Mutex
	nextReplica   int
	inFlight      map[int]*attempt
	attempts      []*attempt // 按发出顺序
	hedges        int
	retries       int
	hedgeTimer    Timer
	deadlineTimer Timer
	finished      bool
}

func newCall(ex *Executor, id int, spec Spec, h *CallHandle) *call {
	return &call{
		ex:       ex,
		id:       id,
		spec:     spec,
		h:        h,
		inFlight: make(map[int]*attempt),
	}
}

func (c *call) start() {
	c.mu.Lock()
	c.ex.logf("call#%d accepted: replicas=%v hedgeDelay=%s maxInFlight=%d deadline=%s idempotent=%v",
		c.id, c.spec.Replicas, c.spec.HedgeDelay, c.spec.MaxInFlight,
		c.spec.Deadline.Format(time.RFC3339Nano), c.spec.Idempotent)
	c.sendLocked(initial)
	c.deadlineTimer = c.ex.clock.AfterFunc(c.spec.Deadline.Sub(c.ex.clock.Now()), c.onDeadline)
	c.mu.Unlock()
}

// sendLocked 向下一副本发出请求（initial/hedge/retry），并重新开始对冲计时。
func (c *call) sendLocked(kind sendKind) {
	replica := c.spec.Replicas[c.nextReplica]
	c.nextReplica++
	id := len(c.attempts)
	a := &attempt{id: id, replica: replica}
	c.attempts = append(c.attempts, a)
	c.inFlight[id] = a
	switch kind {
	case hedge:
		c.hedges++
	case retry:
		c.retries++
	}
	cancel := c.ex.transport.Start(replica, func(resp Response) { c.onResponse(id, resp) })
	a.cancel = cancel
	c.ex.logf("call#%d send: replica=%s kind=%s inFlight=%d hedges=%d retries=%d",
		c.id, replica, kind, len(c.inFlight), c.hedges, c.retries)
	c.restartHedgeTimerLocked()
}

// restartHedgeTimerLocked 自最近一次发出起重新计时；非幂等或副本已用尽则不再计时。
func (c *call) restartHedgeTimerLocked() {
	if c.hedgeTimer != nil {
		c.hedgeTimer.Stop()
		c.hedgeTimer = nil
	}
	if c.finished || !c.spec.Idempotent || c.nextReplica >= len(c.spec.Replicas) {
		return
	}
	c.hedgeTimer = c.ex.clock.AfterFunc(c.spec.HedgeDelay, c.onHedgeTick)
}

func (c *call) onHedgeTick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	switch {
	case c.nextReplica >= len(c.spec.Replicas):
		c.ex.logf("call#%d hedge tick: skip reason=no-more-replicas", c.id)
		return
	case len(c.inFlight) >= c.spec.MaxInFlight:
		c.ex.logf("call#%d hedge tick: skip reason=inflight-full (%d/%d)",
			c.id, len(c.inFlight), c.spec.MaxInFlight)
	case !c.ex.budget.tryHedge():
		c.ex.logf("call#%d hedge tick: skip reason=budget-exhausted", c.id)
	default:
		c.sendLocked(hedge)
		return
	}
	// 本次跳过：再过对冲延迟重新检查（预算或在途数可能变化）。
	c.hedgeTimer = c.ex.clock.AfterFunc(c.spec.HedgeDelay, c.onHedgeTick)
}

func (c *call) onResponse(id int, resp Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		c.ex.logf("call#%d late response discarded: attempt=%d (call already settled)", c.id, id)
		return
	}
	a, ok := c.inFlight[id]
	if !ok {
		c.ex.logf("call#%d late response discarded: attempt=%d (already cancelled)", c.id, id)
		return
	}
	// 恰在截止时刻或之后到达的应答（含成功）一律视为超时。
	if !c.ex.clock.Now().Before(c.spec.Deadline) {
		c.ex.logf("call#%d response at/past deadline discarded: attempt=%d; finishing as timeout", c.id, id)
		c.finishLocked(Outcome{Kind: Timeout, Reason: ErrTimeout})
		return
	}
	delete(c.inFlight, id)
	if resp.Err == nil {
		c.ex.logf("call#%d adopt success: replica=%s value=%v; cancelling %d in-flight",
			c.id, a.replica, resp.Value, len(c.inFlight))
		c.finishLocked(Outcome{
			Kind:    Success,
			Value:   resp.Value,
			Replica: a.replica,
			Hedges:  c.hedges,
			Retries: c.retries,
		})
		return
	}
	a.failed = true
	a.err = resp.Err
	c.ex.logf("call#%d attempt failed: replica=%s err=%v", c.id, a.replica, resp.Err)
	if c.spec.Idempotent && c.nextReplica < len(c.spec.Replicas) {
		c.ex.logf("call#%d retry immediately: next=%s (不占对冲预算)", c.id, c.spec.Replicas[c.nextReplica])
		c.sendLocked(retry)
		return
	}
	if len(c.inFlight) == 0 {
		errs := make([]AttemptError, 0, len(c.attempts))
		for _, at := range c.attempts {
			if at.failed {
				errs = append(errs, AttemptError{Replica: at.replica, Err: at.err})
			}
		}
		c.ex.logf("call#%d all failed: %d error(s) in send order", c.id, len(errs))
		c.finishLocked(Outcome{
			Kind:    AllFailed,
			Reason:  ErrAllFailed,
			Errors:  errs,
			Hedges:  c.hedges,
			Retries: c.retries,
		})
	}
}

func (c *call) onDeadline() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return
	}
	c.ex.logf("call#%d deadline reached; cancelling %d in-flight", c.id, len(c.inFlight))
	c.finishLocked(Outcome{Kind: Timeout, Reason: ErrTimeout, Hedges: c.hedges, Retries: c.retries})
}

// finishLocked 结算唯一结局：停表、取消全部在途、关闭 done。
func (c *call) finishLocked(o Outcome) {
	if c.finished {
		return
	}
	c.finished = true
	if c.hedgeTimer != nil {
		c.hedgeTimer.Stop()
	}
	if c.deadlineTimer != nil {
		c.deadlineTimer.Stop()
	}
	for _, a := range c.attempts {
		if _, ok := c.inFlight[a.id]; !ok {
			continue
		}
		if a.cancel != nil {
			a.cancel()
		}
		delete(c.inFlight, a.id)
	}
	c.ex.logf("call#%d settled: kind=%s replica=%s reason=%v", c.id, o.Kind, o.Replica, o.Reason)
	c.h.outcome = o
	close(c.h.done)
}
