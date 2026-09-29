package scheduler

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"
)

// Policy 决定错过的触发时刻如何补偿。
type Policy int

const (
	// CatchUpAll：按升序执行 (r+1..m] 中最近至多 K 个序号。
	CatchUpAll Policy = iota
	// CatchUpOne：只执行 m。
	CatchUpOne
	// TolerantSkip：仅当 now-t(m) < Tolerance 时执行 m，否则跳过但推进记录。
	TolerantSkip
)

// RejectReason 是可区分的拒绝原因。
type RejectReason string

const (
	ReasonInvalidPeriod    RejectReason = "invalid_period"
	ReasonInvalidK         RejectReason = "invalid_k"
	ReasonInvalidTolerance RejectReason = "invalid_tolerance"
	ReasonStaleTerm        RejectReason = "stale_term"
	ReasonUnauthorized     RejectReason = "unauthorized_instance"
)

// RejectError 携带可区分的拒绝原因；被拒绝的操作不改变执行记录。
type RejectError struct {
	Reason RejectReason
	msg    string
}

func (e *RejectError) Error() string { return e.msg }

// Config 描述一个周期任务协调器。
type Config struct {
	Anchor    time.Time
	Period    time.Duration
	Policy    Policy
	K         int
	Tolerance time.Duration
	// Executor 执行给定序号；返回 error 表示实例在执行中途失效。
	Executor func(seq int) error
	// LogOutput 为判定日志输出位置；默认 os.Stderr。传 io.Discard 可静默。
	LogOutput io.Writer
}

// WakeResult 是一次唤醒的判定与执行结果。
type WakeResult struct {
	Instance string
	Term     int
	Now      time.Time
	M        int // 不晚于 Now 的最大序号；无时刻时为 -1
	R        int // 唤醒前记录中的最后已执行序号
	Skipped  []int
	Executed []int
	// AbortedAt 为执行中途失效时停在的序号；-1 表示未失效。
	AbortedAt int
}

// Coordinator 是多实例周期任务触发协调器。
type Coordinator struct {
	cfg Config
	log *slog.Logger

	mu sync.Mutex
	// execMu 把"认领 → 执行任务体"串行化，使执行顺序与记录推进顺序一致。
	// 它独立于 mu：任务体执行期间不持有 mu，其他实例仍可做时钟判定与授予。
	execMu sync.Mutex
	// grants 记录每个被授任期的属主实例；同时充当"已授出最大任期"索引。
	grants map[int]string
	// 共享执行记录：最后已执行序号 r（初始 -1）及写入它的任期。
	r        int
	rTerm    int
	maxGrant int
}

// New 创建协调器；参数非法时返回带 RejectReason 的错误。
func New(cfg Config) (*Coordinator, error) {
	if cfg.Period <= 0 {
		return nil, &RejectError{ReasonInvalidPeriod,
			fmt.Sprintf("period must be positive, got %v", cfg.Period)}
	}
	if cfg.Policy == CatchUpAll && cfg.K <= 0 {
		return nil, &RejectError{ReasonInvalidK,
			fmt.Sprintf("K must be positive for CatchUpAll, got %d", cfg.K)}
	}
	if cfg.Tolerance < 0 {
		return nil, &RejectError{ReasonInvalidTolerance,
			fmt.Sprintf("tolerance must be non-negative, got %v", cfg.Tolerance)}
	}
	out := cfg.LogOutput
	if out == nil {
		out = os.Stderr
	}
	if cfg.Executor == nil {
		cfg.Executor = func(int) error { return nil }
	}
	return &Coordinator{
		cfg:      cfg,
		log:      slog.New(slog.NewTextHandler(out, nil)),
		grants:   make(map[int]string),
		r:        -1,
		rTerm:    0,
		maxGrant: 0,
	}, nil
}

// Grant 以严格递增的任期向实例授予调度权。
func (c *Coordinator) Grant(term int, instance string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if term <= c.maxGrant {
		err := &RejectError{ReasonStaleTerm,
			fmt.Sprintf("grant term %d must be greater than max granted term %d", term, c.maxGrant)}
		c.log.Warn("grant rejected",
			slog.String("input", fmt.Sprintf("grant(term=%d,instance=%q)", term, instance)),
			slog.String("reason", string(ReasonStaleTerm)),
			slog.Int("maxGrant", c.maxGrant))
		return err
	}
	c.grants[term] = instance
	c.maxGrant = term
	c.log.Info("grant accepted",
		slog.String("input", fmt.Sprintf("grant(term=%d,instance=%q)", term, instance)),
		slog.String("output", "scheduling authority granted"),
		slog.Int("maxGrant", c.maxGrant))
	return nil
}

// Wake 唤醒某实例；并发安全。
func (c *Coordinator) Wake(instance string, now time.Time) (WakeResult, error) {
	c.mu.Lock()
	term, ok := c.termOfLocked(instance)
	if !ok {
		c.mu.Unlock()
		err := &RejectError{ReasonUnauthorized,
			fmt.Sprintf("instance %q holds no granted term", instance)}
		c.log.Warn("wake rejected",
			slog.String("input", fmt.Sprintf("wake(instance=%q,now=%s)", instance, now.Format(time.RFC3339Nano))),
			slog.String("reason", string(ReasonUnauthorized)))
		return WakeResult{}, err
	}

	res := WakeResult{Instance: instance, Term: term, Now: now, M: -1, R: c.r, AbortedAt: -1}

	// 本地时钟求不晚于 now 的最大序号 m = floor((now-anchor)/period)。
	if d := now.Sub(c.cfg.Anchor); d >= 0 {
		res.M = int(d / c.cfg.Period)
	}

	// m 不存在或不大于 r：什么也不做。
	if res.M <= c.r {
		c.log.Info("wake noop",
			slog.String("input", c.fmtInput(instance, term, now)),
			slog.String("decision", "m_missing_or_not_ahead_of_r"),
			slog.Int("m", res.M), slog.Int("r", c.r))
		c.mu.Unlock()
		return res, nil
	}

	// 自身任期小于记录任期：放弃（记录被更高任期写过）。
	if term < c.rTerm {
		c.log.Info("wake yield",
			slog.String("input", c.fmtInput(instance, term, now)),
			slog.String("decision", "term_less_than_record_term"),
			slog.Int("term", term), slog.Int("recordTerm", c.rTerm), slog.Int("m", res.M), slog.Int("r", c.r))
		c.mu.Unlock()
		return res, nil
	}

	// 依据策略确定本轮要处理的序号与判定依据。
	run, skip, basis := c.planLocked(res.M, now)

	c.mu.Unlock()

	// 逐序号原子认领：在锁内做任期检查 + 推进记录，锁外执行任务体。
	// 因此"执行中途失效"的序号已被推进记录，任何实例都不会重做。
	preempted := false
	for _, seq := range run {
		c.execMu.Lock()
		c.mu.Lock()
		if term < c.rTerm || seq <= c.r {
			curR, curTerm := c.r, c.rTerm
			c.mu.Unlock()
			c.execMu.Unlock()
			preempted = true
			c.log.Info("wake preempted",
				slog.String("input", c.fmtInput(instance, term, now)),
				slog.String("decision", "higher_term_or_already_advanced"),
				slog.Int("atSeq", seq), slog.Int("r", curR), slog.Int("recordTerm", curTerm))
			break
		}
		c.r = seq
		c.rTerm = term
		c.mu.Unlock()

		c.log.Info("execute begin",
			slog.String("input", c.fmtInput(instance, term, now)),
			slog.Int("seq", seq), slog.String("basis", basis))
		execErr := c.cfg.Executor(seq)
		c.execMu.Unlock()
		if execErr != nil {
			res.AbortedAt = seq
			res.Executed = append(res.Executed, seq)
			c.log.Error("execute failed mid-flight; record already advanced, no redo",
				slog.Int("seq", seq), slog.Int("r", seq), slog.String("err", execErr.Error()))
			res.Skipped = skip
			return res, nil
		}
		res.Executed = append(res.Executed, seq)
		c.log.Info("execute done", slog.Int("seq", seq))
	}

	// 全部补偿窗口外、或容忍跳过策略放弃执行的序号：记录仍需推进到 m。
	// 若本唤醒被高任期抢占中断，剩余序号留给新任期判定，这里不得代为推进。
	c.mu.Lock()
	if !preempted && c.r < res.M && term >= c.rTerm {
		c.r = res.M
		c.rTerm = term
	}
	res.R = c.r
	c.mu.Unlock()

	res.Skipped = skip
	c.log.Info("wake complete",
		slog.String("input", c.fmtInput(instance, term, now)),
		slog.String("output", fmt.Sprintf("executed=%v skipped=%v abortedAt=%d", res.Executed, skip, res.AbortedAt)),
		slog.String("basis", basis),
		slog.Int("m", res.M), slog.Int("r", res.R))
	return res, nil
}

// LastExecuted 返回共享记录快照（最后已执行序号与其写入任期）。
func (c *Coordinator) LastExecuted() (r int, term int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.r, c.rTerm
}

// termOfLocked 返回实例当前持有的（它被授予的）任期。
func (c *Coordinator) termOfLocked(instance string) (int, bool) {
	var term int
	found := false
	for t, who := range c.grants {
		if who == instance && (!found || t > term) {
			term = t
			found = true
		}
	}
	return term, found
}

// planLocked 按策略给出 (要执行的升序序号, 跳过不执行的序号, 判定依据)。
func (c *Coordinator) planLocked(m int, now time.Time) (run, skip []int, basis string) {
	r := c.r
	switch c.cfg.Policy {
	case CatchUpAll:
		start := r + 1
		if m-r > c.cfg.K {
			start = m - c.cfg.K + 1
			for s := r + 1; s < start; s++ {
				skip = append(skip, s)
			}
		}
		for s := start; s <= m; s++ {
			run = append(run, s)
		}
		basis = fmt.Sprintf("catch_up_all: latest min(K=%d, m-r=%d) seqs in ascending order", c.cfg.K, m-r)
	case CatchUpOne:
		run = []int{m}
		if r+1 <= m-1 {
			for s := r + 1; s < m; s++ {
				skip = append(skip, s)
			}
		}
		basis = "catch_up_one: execute only m"
	case TolerantSkip:
		tm := c.cfg.Anchor.Add(time.Duration(m) * c.cfg.Period)
		latency := now.Sub(tm)
		if latency < c.cfg.Tolerance {
			run = []int{m}
			basis = fmt.Sprintf("tolerant_skip: latency %v < tolerance %v, execute m", latency, c.cfg.Tolerance)
		} else {
			basis = fmt.Sprintf("tolerant_skip: latency %v >= tolerance %v, skip m but advance record", latency, c.cfg.Tolerance)
		}
		for s := r + 1; s < m; s++ {
			skip = append(skip, s)
		}
		if latency >= c.cfg.Tolerance {
			skip = append(skip, m)
		}
	}
	sort.Ints(run)
	sort.Ints(skip)
	return run, skip, basis
}

func (c *Coordinator) fmtInput(instance string, term int, now time.Time) string {
	return fmt.Sprintf("wake(instance=%q,term=%d,now=%s)", instance, term, now.Format(time.RFC3339Nano))
}
