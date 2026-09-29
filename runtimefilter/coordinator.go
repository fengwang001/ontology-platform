package runtimefilter

import (
	"cmp"
	"sync"
	"time"
)

// filterable 表示探测侧不是保留侧、运行时过滤不改变结果的连接类型：
// 内连接与半连接。其余连接类型（左外/右外/全外/反连接）探测侧是保留侧，
// 被过滤掉的行仍可能以未匹配扩展行出现在结果中，故一律不允许过滤。
func filterable(jt JoinType) bool {
	switch jt {
	case JoinInner, JoinSemi:
		return true
	default:
		return false
	}
}

// Filterable 暴露连接类型可否过滤的推导结果。
func Filterable(jt JoinType) bool { return filterable(jt) }

// Coordinator 收集构建侧分片摘要并向探测扫描发布不可变过滤器版本。
type Coordinator[K cmp.Ordered] struct {
	cfg     Config[K]
	enabled bool

	mu       sync.Mutex
	reported []bool
	received int
	// 增量合并态：仅在未就绪期间使用。
	gotMinMax bool
	min       K
	max       K
	distinct  map[K]struct{}
	abandoned bool
	// 发布态：ready 后只读替换；cur 为 nil 表示过滤器不存在/已作废。
	ready   bool
	voided  bool
	cur     *filter[K]
	version int
	readyCh chan struct{}
}

// NewCoordinator 创建协调器；连接类型不允许过滤或参数非法时返回哨兵错误。
func NewCoordinator[K cmp.Ordered](cfg Config[K]) (*Coordinator[K], error) {
	if !joinKnown(cfg.JoinType) {
		return nil, ErrUnknownJoin
	}
	if cfg.ShardCount <= 0 {
		return nil, ErrShardOutOfRange
	}
	if cfg.MaxDistinct < 0 {
		return nil, ErrShardOutOfRange
	}
	if cfg.ReadyWait <= 0 {
		cfg.ReadyWait = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	c := &Coordinator[K]{
		cfg:      cfg,
		enabled:  filterable(cfg.JoinType),
		reported: make([]bool, cfg.ShardCount),
		distinct: make(map[K]struct{}),
		readyCh:  make(chan struct{}),
	}
	return c, nil
}

// Report 接收一个分片报告。被拒绝的报告不改变任何状态。
func (c *Coordinator[K]) Report(report ShardReport[K]) error {
	if report.Shard < 0 || report.Shard >= c.cfg.ShardCount {
		c.logf("report rejected shard=%d reason=out-of-range state-unchanged", report.Shard)
		return ErrShardOutOfRange
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reported[report.Shard] {
		c.logfLocked("report rejected shard=%d reason=duplicate state-unchanged", report.Shard)
		return ErrDuplicateShard
	}

	// 已被接受的报告不可回滚：先落账再合并。
	c.reported[report.Shard] = true
	c.received++

	if report.Abandon {
		c.abandoned = true
	} else if report.Empty {
		// 空分片不参与最小/最大值与去重集合的合并。
	} else {
		if !c.gotMinMax {
			c.gotMinMax = true
			c.min = report.Min
			c.max = report.Max
		} else {
			if report.Min < c.min {
				c.min = report.Min
			}
			if report.Max > c.max {
				c.max = report.Max
			}
		}
		for k := range report.Distinct {
			c.distinct[k] = struct{}{}
		}
	}

	if c.received == c.cfg.ShardCount {
		c.publishLocked()
	}

	action := "accepted"
	if c.ready {
		action = "accepted filter-ready"
	}
	c.logfLocked("report shard=%d %s received=%d/%d abandoned=%t",
		report.Shard, action, c.received, c.cfg.ShardCount, c.abandoned)
	return nil
}

// NewScanner 为一个探测侧扫描注册扫描器。
func (c *Coordinator[K]) NewScanner(name string, keyOf KeyOf[K]) *Scanner[K] {
	return &Scanner[K]{coord: c, name: name, keyOf: keyOf}
}

// readySnapshot 报告过滤器是否已经发布，供测试与外部观测使用。
func (c *Coordinator[K]) readySnapshot() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

// publishLocked 在最后一片报告到达时合并发布一个不可变过滤器版本。
func (c *Coordinator[K]) publishLocked() {
	c.version++
	c.ready = true
	if c.abandoned {
		// 任一分片放弃：过滤器作废，探测侧全部放行。
		c.voided = true
		c.cur = nil
		c.logfLocked("filter voided version=%d reason=shard-abandoned", c.version)
	} else {
		f := &filter[K]{
			version:    c.version,
			min:        c.min,
			max:        c.max,
			distinct:   c.distinct,
			buildEmpty: !c.gotMinMax,
		}
		if len(f.distinct) > c.cfg.MaxDistinct {
			// 去重键数超上限：降级为仅保留最小最大值。
			f.distinct = nil
		}
		c.cur = f
		mode := "distinct"
		if f.distinct == nil {
			mode = "minmax(degraded)"
		}
		if f.buildEmpty {
			mode = "build-empty"
		}
		c.logfLocked("filter ready version=%d mode=%s min=%v max=%v distinct=%d",
			f.version, mode, minOrNil(c), maxOrNil(c), len(c.distinct))
	}
	close(c.readyCh)
}

// waitReady 阻塞直到过滤器就绪或等待时限到达；返回当前过滤器快照（可能为 nil）。
// 每一批只使用一个完整版本：快照在整批开始时取得，中途发布新版本也不影响本批。
func (c *Coordinator[K]) waitReady(deadline time.Time) *filter[K] {
	if !c.enabled {
		return nil
	}
	c.mu.Lock()
	if c.ready {
		f := c.cur
		c.mu.Unlock()
		return f
	}
	ch := c.readyCh
	wait := c.cfg.ReadyWait
	if !deadline.IsZero() {
		if d := time.Until(deadline); d < wait {
			wait = d
		}
	}
	c.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	select {
	case <-ch:
		c.mu.Lock()
		f := c.cur
		c.mu.Unlock()
		return f
	case <-c.cfg.After(wait):
		return nil
	}
}

func (c *Coordinator[K]) logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logfLocked(format, args...)
}

func (c *Coordinator[K]) logfLocked(format string, args ...any) {
	if c.cfg.Logger != nil {
		c.cfg.Logger.Logf(format, args...)
	}
}

func joinKnown(jt JoinType) bool {
	switch jt {
	case JoinInner, JoinLeftOuter, JoinRightOuter, JoinFullOuter, JoinSemi, JoinAnti:
		return true
	default:
		return false
	}
}

func minOrNil[K cmp.Ordered](c *Coordinator[K]) any {
	if c.gotMinMax {
		return c.min
	}
	return nil
}

func maxOrNil[K cmp.Ordered](c *Coordinator[K]) any {
	if c.gotMinMax {
		return c.max
	}
	return nil
}
