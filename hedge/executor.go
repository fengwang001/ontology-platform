package hedge

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// 参数校验错误，彼此可区分。
var (
	ErrNoReplicas             = errors.New("hedge: replica list is empty")
	ErrDuplicateReplica       = errors.New("hedge: replica list contains duplicates")
	ErrNonPositiveHedgeDelay  = errors.New("hedge: hedge delay must be positive")
	ErrNonPositiveMaxInFlight = errors.New("hedge: max in-flight must be positive")
	ErrDeadlinePassed         = errors.New("hedge: deadline already passed")
)

// ErrTimeout 是截止时间到达时的结局。
var ErrTimeout = errors.New("hedge: deadline exceeded")

// Outcome 是一次调用的唯一结局。
type Outcome int

const (
	OutcomeSuccess Outcome = iota
	OutcomeAllFailed
	OutcomeTimeout
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "success"
	case OutcomeAllFailed:
		return "all-failed"
	case OutcomeTimeout:
		return "timeout"
	}
	return "unknown"
}

// SendFunc 向指定副本发起一次请求；ctx 取消时应尽快返回。
type SendFunc func(ctx context.Context, replica string) (any, error)

// Request 是一次对冲执行的输入。
type Request struct {
	Replicas    []string      // 有序副本列表，每个至多发一次
	HedgeDelay  time.Duration // 自最近一次发出起的对冲检查间隔
	MaxInFlight int           // 最大在途数
	Deadline    time.Time     // 截止时间（以注入时钟为准）
	Idempotent  bool          // 非幂等请求只发首个副本
}

// AttemptError 记录单个副本的错误。
type AttemptError struct {
	Replica string
	Err     error
}

// Result 是一次调用的输出，结局唯一。
type Result struct {
	Outcome Outcome
	Replica string         // 成功时采用的副本
	Value   any            // 成功时的返回值
	Errors  []AttemptError // 全部失败时按发出顺序排列的错误
}

// Stats 是执行器的全局统计。
type Stats struct {
	Accepted int64 // 已通过校验、被接受的调用总数
	Hedges   int64 // 已发出的对冲总数
}

// Executor 在并发调用间共享对冲预算。
type Executor struct {
	clock   Clock
	baseCap int
	ratio   float64

	mu       sync.Mutex
	accepted int64
	hedges   int64
}

// NewExecutor 创建执行器。预算上限为 floor(baseCap + accepted*ratio)。
func NewExecutor(clock Clock, baseCap int, ratio float64) *Executor {
	return &Executor{clock: clock, baseCap: baseCap, ratio: ratio}
}

// Stats 返回当前统计快照。
func (e *Executor) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{Accepted: e.accepted, Hedges: e.hedges}
}

// Budget 返回当前允许发出的对冲总数上限。
func (e *Executor) Budget() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.budgetLocked()
}

func (e *Executor) budgetLocked() int64 {
	return int64(math.Floor(float64(e.baseCap) + float64(e.accepted)*e.ratio))
}

func (e *Executor) accept() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.accepted++
}

// tryHedge 在预算允许时占有一次对冲额度。
func (e *Executor) tryHedge() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hedges < e.budgetLocked() {
		e.hedges++
		return true
	}
	return false
}

func validateRequest(req Request, now time.Time) error {
	if len(req.Replicas) == 0 {
		return ErrNoReplicas
	}
	seen := make(map[string]struct{}, len(req.Replicas))
	for _, r := range req.Replicas {
		if _, dup := seen[r]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateReplica, r)
		}
		seen[r] = struct{}{}
	}
	if req.HedgeDelay <= 0 {
		return ErrNonPositiveHedgeDelay
	}
	if req.MaxInFlight <= 0 {
		return ErrNonPositiveMaxInFlight
	}
	if !now.Before(req.Deadline) {
		return ErrDeadlinePassed
	}
	return nil
}

type attemptResult struct {
	idx     int
	replica string
	value   any
	err     error
}

type attemptError struct {
	idx     int
	replica string
	err     error
}

// Execute 执行一次调用，恰好返回一个结局。
func (e *Executor) Execute(ctx context.Context, req Request, send SendFunc) (Result, error) {
	now := e.clock.Now()
	if err := validateRequest(req, now); err != nil {
		return Result{}, err
	}
	e.accept()

	// 缓冲足够大，迟到结果的发送永不阻塞，直接丢弃。
	respCh := make(chan attemptResult, len(req.Replicas))
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	inFlight := 0
	nextReplica := 0
	var errs []attemptError

	var hedgeTimer Timer
	var hedgeC <-chan time.Time
	var nextCheck time.Time

	stopHedge := func() {
		if hedgeTimer != nil {
			hedgeTimer.Stop()
			hedgeTimer = nil
			hedgeC = nil
		}
	}
	scheduleCheck := func(at time.Time) {
		d := at.Sub(e.clock.Now())
		if d < 0 {
			d = 0
		}
		if hedgeTimer != nil {
			hedgeTimer.Stop()
		}
		hedgeTimer = e.clock.NewTimer(d)
		hedgeC = hedgeTimer.C()
	}
	sendAttempt := func(idx int, at time.Time) {
		inFlight++
		// 先重启对冲计时再发出请求：发出动作对外可见时计时器已就绪。
		if req.Idempotent && idx+1 < len(req.Replicas) {
			nextCheck = at.Add(req.HedgeDelay)
			scheduleCheck(nextCheck)
		} else {
			stopHedge()
		}
		replica := req.Replicas[idx]
		go func() {
			v, err := send(callCtx, replica)
			respCh <- attemptResult{idx: idx, replica: replica, value: v, err: err}
		}()
	}

	deadlineTimer := e.clock.NewTimer(req.Deadline.Sub(now))
	defer deadlineTimer.Stop()

	timeout := func() (Result, error) {
		return Result{Outcome: OutcomeTimeout}, nil
	}

	sendAttempt(0, now)
	nextReplica = 1

	for {
		// 循环入口先判定截止，保证同刻事件下结论确定。
		if !e.clock.Now().Before(req.Deadline) {
			return timeout()
		}
		select {
		case r := <-respCh:
			arrival := e.clock.Now()
			if !arrival.Before(req.Deadline) {
				// 恰在截止时刻或更晚到达的结果一律视为超时。
				return timeout()
			}
			inFlight--
			if r.err == nil {
				return Result{Outcome: OutcomeSuccess, Replica: r.replica, Value: r.value}, nil
			}
			errs = append(errs, attemptError{idx: r.idx, replica: r.replica, err: r.err})
			canSendMore := req.Idempotent && nextReplica < len(req.Replicas)
			switch {
			case canSendMore:
				// 失败立即改投下一副本：计为重试，不占对冲预算。
				sendAttempt(nextReplica, arrival)
				nextReplica++
			case inFlight == 0:
				return Result{Outcome: OutcomeAllFailed, Errors: orderedErrors(errs)}, nil
			}
		case <-hedgeC:
			tick := e.clock.Now()
			if !tick.Before(req.Deadline) {
				return timeout()
			}
			if nextReplica < len(req.Replicas) && inFlight < req.MaxInFlight && e.tryHedge() {
				sendAttempt(nextReplica, tick)
				nextReplica++
			} else {
				// 跳过本次检查，锚定上次发出时刻继续下一周期。
				nextCheck = nextCheck.Add(req.HedgeDelay)
				scheduleCheck(nextCheck)
			}
		case <-deadlineTimer.C():
			return timeout()
		}
	}
}

func orderedErrors(errs []attemptError) []AttemptError {
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].idx < errs[j].idx })
	out := make([]AttemptError, len(errs))
	for i, e := range errs {
		out[i] = AttemptError{Replica: e.replica, Err: e.err}
	}
	return out
}
