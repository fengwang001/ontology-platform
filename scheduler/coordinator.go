// Package coordinator 实现多实例周期任务触发协调器。
//
// 多个实例在同一组周期锚点上竞争执行权，通过单调递增的“任期”
// 与一份共享执行记录保证：每个时刻全局至多执行一次，任期转移、
// 时钟跳变与实例失效下行为可预测、可重放。
package coordinator

import (
	"fmt"
	"strings"
	"sync"
)

// Strategy 决定错过的周期时刻如何补偿。
type Strategy int

const (
	// CatchUpAll 全部补偿：按升序执行 r+1..m 中最近的至多 K 个时刻。
	CatchUpAll Strategy = iota + 1
	// CatchUpOne 补一次：只执行当前最大序号 m。
	CatchUpOne
	// TolerantSkip 容忍跳过：仅在容忍时长内执行 m，否则跳过但推进记录。
	TolerantSkip
)

func (s Strategy) valid() bool { return s >= CatchUpAll && s <= TolerantSkip }

// RejectReason 以可区分的枚举说明操作被整体拒绝的原因。
type RejectReason int

const (
	RejectInvalidPeriod RejectReason = iota + 1
	RejectInvalidK
	RejectNegativeTolerance
	RejectUnknownStrategy
	RejectTermNotGreater
	RejectUnauthorizedInstance
	RejectStaleTerm
)

// RejectError 表示一次被整体拒绝的操作；被拒绝的操作不会改变执行记录。
type RejectError struct {
	Reason RejectReason
	Op     string
	Detail string
}

func (e *RejectError) Error() string { return e.Op + ": " + e.Detail }

func reject(op string, reason RejectReason, detail string) *RejectError {
	return &RejectError{Reason: reason, Op: op, Detail: detail}
}

// Config 是协调器的静态配置。
type Config struct {
	Anchor    int64    // 第 0 个时刻（时间单位由调用方约定）
	Period    int64    // 相邻时刻间隔，必须为正
	K         int      // CatchUpAll 最多补偿的时刻数，必须为正
	Tolerance int64    // TolerantSkip 的容忍时长，不得为负
	Strategy  Strategy // 补偿策略
}

// ItemKind 描述计划项的处置方式。
type ItemKind int

const (
	ItemExecute ItemKind = iota + 1 // 执行该时刻
	ItemSkip                        // 超出容忍窗口：不执行
	ItemTooOld                      // 超出补偿上限 K：不执行
)

// PlanItem 是序号 r+1..m 中某个时刻的处置决定。
type PlanItem struct {
	Seq  int64
	Kind ItemKind
}

// Result 是一次 Wakeup 的结果。
type Result struct {
	Instance  string
	Term      int64
	Now       int64
	M         int64 // 不晚于 Now 的最大序号；无时刻时为 -1
	RBefore   int64 // 进入时记录中的最后已执行序号
	Plan      []PlanItem
	Executed  []int64 // 实际执行成功的序号，按记录推进先后严格递增
	Failed    *int64  // 执行中途失效的序号（若有）
	Abandoned bool    // 是否因记录被更高任期写入而中途放弃
	RAfter    int64   // 离开时记录中的最后已执行序号
}

// ExecFn 是单个时刻的任务回调。回调返回 error 表示该实例在执行中失效。
type ExecFn func(seq int64, t int64) error

// Record 是各实例共享的执行记录。
type Record struct {
	LastSeq   int64 // 最后已推进序号 r，初始为 -1
	WriteTerm int64 // 写入该记录的任期
}

// Coordinator 是多实例周期任务触发协调器。
type Coordinator struct {
	cfg Config

	mu      sync.Mutex
	record  Record
	grants  map[int64]string // term -> 被授予的实例
	maxTerm int64            // 已授出的最大任期
	log     []string
	seqNum  int
}

// New 创建协调器并校验静态配置；配置非法时返回带 RejectReason 的错误。
func New(cfg Config) (*Coordinator, error) {
	if cfg.Period <= 0 {
		return nil, reject("New", RejectInvalidPeriod,
			fmt.Sprintf("period must be positive, got %d", cfg.Period))
	}
	if cfg.K <= 0 {
		return nil, reject("New", RejectInvalidK,
			fmt.Sprintf("K must be positive, got %d", cfg.K))
	}
	if cfg.Tolerance < 0 {
		return nil, reject("New", RejectNegativeTolerance,
			fmt.Sprintf("tolerance must be non-negative, got %d", cfg.Tolerance))
	}
	if !cfg.Strategy.valid() {
		return nil, reject("New", RejectUnknownStrategy,
			fmt.Sprintf("unknown strategy %d", cfg.Strategy))
	}
	return &Coordinator{
		cfg:    cfg,
		record: Record{LastSeq: -1, WriteTerm: 0},
		grants: make(map[int64]string),
	}, nil
}

// Grant 以严格递增的任期向某实例授予调度权。
func (c *Coordinator) Grant(instance string, term int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if instance == "" {
		err := reject("Grant", RejectUnauthorizedInstance, "instance id must not be empty")
		c.logLocked("grant  input={instance:%q term:%d} output=rejected reason=%s", instance, term, reasonName(err.Reason))
		return err
	}
	if term <= c.maxTerm {
		err := reject("Grant", RejectTermNotGreater,
			fmt.Sprintf("term %d is not greater than max granted term %d", term, c.maxTerm))
		c.logLocked("grant  input={instance:%q term:%d} output=rejected reason=%s", instance, term, reasonName(err.Reason))
		return err
	}
	c.maxTerm = term
	c.grants[term] = instance
	c.logLocked("grant  input={instance:%q term:%d} output=accepted max_term=%d 判定依据=任期严格递增且实例已登记",
		instance, term, c.maxTerm)
	return nil
}

// Wakeup 处理一次实例唤醒。返回被拒绝的原因时执行记录保持不变。
func (c *Coordinator) Wakeup(instance string, term int64, now int64, exec ExecFn) (*Result, error) {
	res := &Result{Instance: instance, Term: term, Now: now, M: -1, RBefore: -1, RAfter: -1}

	// 1) 授权校验：该任期必须确实授予给了该实例。
	c.mu.Lock()
	owner, ok := c.grants[term]
	if !ok || owner != instance {
		err := reject("Wakeup", RejectUnauthorizedInstance,
			fmt.Sprintf("instance %q is not authorized for term %d", instance, term))
		c.logLocked("wakeup input={instance:%q term:%d now:%d} output=rejected reason=%s 判定依据=任期未授予该实例",
			instance, term, now, reasonName(err.Reason))
		c.mu.Unlock()
		return nil, err
	}
	// 2) 任期栅栏：记录已被更高任期写入则整体放弃，记录不变。
	if term < c.record.WriteTerm {
		err := reject("Wakeup", RejectStaleTerm,
			fmt.Sprintf("term %d < record write term %d", term, c.record.WriteTerm))
		c.logLocked("wakeup input={instance:%q term:%d now:%d} output=rejected reason=%s 判定依据=自身任期小于记录任期",
			instance, term, now, reasonName(err.Reason))
		c.mu.Unlock()
		return nil, err
	}
	r := c.record.LastSeq
	c.mu.Unlock()

	res.RBefore = r

	// 3) 按本地时钟求不晚于 now 的最大序号 m。
	if now < c.cfg.Anchor {
		res.M = -1
		res.RAfter = r
		c.appendLog("wakeup input={instance:%q term:%d now:%d} output=no-op m=none r=%d 判定依据=now早于锚点尚无时刻",
			instance, term, now, r)
		return res, nil
	}
	m := (now - c.cfg.Anchor) / c.cfg.Period
	res.M = m
	if m <= r {
		res.RAfter = r
		c.appendLog("wakeup input={instance:%q term:%d now:%d} output=no-op m=%d r=%d 判定依据=m不大于r没有新时刻",
			instance, term, now, m, r)
		return res, nil
	}

	// 4) 依据策略构造 r+1..m 的处置计划（仅决定，尚未改记录）。
	res.Plan = c.buildPlan(r, m, now)
	c.appendLog("wakeup input={instance:%q term:%d now:%d} r=%d m=%d strategy=%s plan=%s",
		instance, term, now, r, m, strategyName(c.cfg.Strategy), formatPlan(res.Plan))

	// 5) 逐项：先在锁内原子推进记录，再在锁外执行回调。
	for _, item := range res.Plan {
		c.mu.Lock()
		// 高任期栅栏：记录可能已被并发的更高任期实例写过。
		if term < c.record.WriteTerm {
			res.Abandoned = true
			res.RAfter = c.record.LastSeq
			c.mu.Unlock()
			c.appendLog("wakeup instance=%q term=%d 放弃于seq=%d 判定依据=记录已被更高任期写入", instance, term, item.Seq)
			return res, nil
		}
		// 该序号可能已被并发实例推进（同任期或更早），绝不重做。
		if item.Seq <= c.record.LastSeq {
			c.mu.Unlock()
			continue
		}
		c.record = Record{LastSeq: item.Seq, WriteTerm: term}
		c.logLocked("advance seq=%d term=%d instance=%q kind=%s 判定依据=原子推进记录先于执行保证失效不重做",
			item.Seq, term, instance, kindName(item.Kind))
		c.mu.Unlock()

		if item.Kind != ItemExecute {
			continue
		}
		if exec == nil {
			res.Executed = append(res.Executed, item.Seq)
			continue
		}
		if err := exec(item.Seq, c.timeAt(item.Seq)); err != nil {
			failed := item.Seq
			res.Failed = &failed
			c.mu.Lock()
			res.RAfter = c.record.LastSeq
			c.mu.Unlock()
			c.appendLog("execute seq=%d instance=%q output=failed err=%q 判定依据=记录已推进任何实例不得重做停止后续处理",
				item.Seq, instance, err.Error())
			return res, fmt.Errorf("execute seq %d failed: %w", item.Seq, err)
		}
		res.Executed = append(res.Executed, item.Seq)
		c.appendLog("execute seq=%d t=%d instance=%q output=done", item.Seq, c.timeAt(item.Seq), instance)
	}

	c.mu.Lock()
	res.RAfter = c.record.LastSeq
	c.mu.Unlock()
	c.appendLog("wakeup input={instance:%q term:%d now:%d} output={executed:%v failed:%s abandoned:%t r:%d->%d}",
		instance, term, now, res.Executed, failedName(res.Failed), res.Abandoned, r, res.RAfter)
	return res, nil
}

// Snapshot 返回执行记录的只读副本。
func (c *Coordinator) Snapshot() Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.record
}

// LogLines 返回按发生顺序排列的判定日志。
func (c *Coordinator) LogLines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.log))
	copy(out, c.log)
	return out
}

// timeAt 返回第 seq 个时刻：anchor + seq*period。
func (c *Coordinator) timeAt(seq int64) int64 { return c.cfg.Anchor + seq*c.cfg.Period }

// buildPlan 按所选策略决定序号 r+1..m 中每个时刻的处置方式。
func (c *Coordinator) buildPlan(r, m, now int64) []PlanItem {
	plan := make([]PlanItem, 0, m-r)
	switch c.cfg.Strategy {
	case CatchUpAll:
		first := r + 1
		if m-r > int64(c.cfg.K) {
			cutoff := m - int64(c.cfg.K) // r+1..cutoff 太旧，只推进不执行
			for seq := r + 1; seq <= cutoff; seq++ {
				plan = append(plan, PlanItem{Seq: seq, Kind: ItemTooOld})
			}
			first = cutoff + 1
		}
		for seq := first; seq <= m; seq++ {
			plan = append(plan, PlanItem{Seq: seq, Kind: ItemExecute})
		}
	case CatchUpOne:
		for seq := r + 1; seq < m; seq++ {
			plan = append(plan, PlanItem{Seq: seq, Kind: ItemTooOld})
		}
		plan = append(plan, PlanItem{Seq: m, Kind: ItemExecute})
	case TolerantSkip:
		for seq := r + 1; seq < m; seq++ {
			plan = append(plan, PlanItem{Seq: seq, Kind: ItemTooOld})
		}
		kind := ItemExecute
		if now-c.timeAt(m) >= c.cfg.Tolerance {
			kind = ItemSkip
		}
		plan = append(plan, PlanItem{Seq: m, Kind: kind})
	}
	return plan
}

func (c *Coordinator) appendLog(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logLocked(format, args...)
}

// logLocked 在已持有 c.mu 时追加日志，保证并发下日志顺序与判定顺序一致。
func (c *Coordinator) logLocked(format string, args ...any) {
	c.seqNum++
	c.log = append(c.log, fmt.Sprintf("#%03d %s", c.seqNum, fmt.Sprintf(format, args...)))
}

func reasonName(r RejectReason) string {
	switch r {
	case RejectInvalidPeriod:
		return "InvalidPeriod"
	case RejectInvalidK:
		return "InvalidK"
	case RejectNegativeTolerance:
		return "NegativeTolerance"
	case RejectUnknownStrategy:
		return "UnknownStrategy"
	case RejectTermNotGreater:
		return "TermNotGreater"
	case RejectUnauthorizedInstance:
		return "UnauthorizedInstance"
	case RejectStaleTerm:
		return "StaleTerm"
	default:
		return "Unknown"
	}
}

func strategyName(s Strategy) string {
	switch s {
	case CatchUpAll:
		return "CatchUpAll"
	case CatchUpOne:
		return "CatchUpOne"
	case TolerantSkip:
		return "TolerantSkip"
	default:
		return "Unknown"
	}
}

func kindName(k ItemKind) string {
	switch k {
	case ItemExecute:
		return "execute"
	case ItemSkip:
		return "skip"
	case ItemTooOld:
		return "too-old"
	default:
		return "unknown"
	}
}

func formatPlan(p []PlanItem) string {
	parts := make([]string, len(p))
	for i, item := range p {
		parts[i] = fmt.Sprintf("%d:%s", item.Seq, kindName(item.Kind))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func failedName(f *int64) any {
	if f == nil {
		return "none"
	}
	return *f
}
