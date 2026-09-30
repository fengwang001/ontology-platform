package runtimefilter

import (
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Row 是探测侧扫描到的一行。KeyNull 为 true 表示连接键为空。
type Row struct {
	Key     int64
	KeyNull bool
	Payload string
}

// BatchStats 记录一批的过滤统计，恒有 Dropped+Passed == Scanned。
type BatchStats struct {
	Scanned int
	Passed  int
	Dropped int
	// Reason 记录本批判定依据，便于日志与测试核对。
	Reason string
}

// Coordinator 协调构建侧分片报告与探测侧批过滤，可并发调用。
type Coordinator struct {
	joinType    JoinType
	maxDistinct int
	waitTimeout time.Duration
	logger      *log.Logger

	mu        sync.Mutex
	summaries []ShardSummary
	reported  []bool
	remaining int

	// ready 在过滤器定稿（就绪或作废）时关闭。
	ready chan struct{}
	// current 是已发布的过滤器快照，原子替换，探测侧每批只加载一次。
	current atomic.Pointer[Filter]
}

// NewCoordinator 创建协调器。joinType 决定探测侧是否允许过滤；
// maxDistinct 为合并后去重键数上限；waitTimeout 为每批等待就绪的时限。
func NewCoordinator(joinType JoinType, shards int, maxDistinct int, waitTimeout time.Duration, logger *log.Logger) *Coordinator {
	if shards <= 0 {
		shards = 1
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Coordinator{
		joinType:    joinType,
		maxDistinct: maxDistinct,
		waitTimeout: waitTimeout,
		logger:      logger,
		summaries:   make([]ShardSummary, shards),
		reported:    make([]bool, shards),
		remaining:   shards,
		ready:       make(chan struct{}),
	}
}

// ReportShard 处理一个构建分片的摘要报告。
// 连接类型未知、分片编号越界、同一分片重复报告都会整体拒绝并返回
// 可区分错误，被拒绝的报告不改变任何状态。
func (c *Coordinator) ReportShard(shard int, s ShardSummary) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.joinType == JoinUnknown {
		return ErrUnknownJoinType
	}
	if shard < 0 || shard >= len(c.summaries) {
		return ErrShardOutOfRange
	}
	if c.reported[shard] {
		return ErrDuplicateShard
	}
	c.summaries[shard] = s
	c.reported[shard] = true
	c.remaining--
	c.logger.Printf("report shard=%d remaining=%d aborted=%v empty=%v keys=%d",
		shard, c.remaining, s.Aborted, s.Empty, len(s.Keys))
	if c.remaining > 0 {
		return nil
	}
	f := merge(c.summaries, c.maxDistinct)
	c.current.Store(f)
	close(c.ready)
	c.logger.Printf("filter ready invalid=%v empty=%v degraded=%v min=%d max=%d keys=%d",
		f.Invalid, f.Empty, f.Degraded, f.Min, f.Max, len(f.Keys))
	return nil
}

// filterable 推导探测侧是否允许过滤：仅当探测侧不是保留侧。
// 内连接、半连接与右外连接（保留侧为构建侧）可过滤；
// 探测侧为保留侧的左外连接、全外连接与反连接一律不过滤。
func (c *Coordinator) filterable() bool {
	switch c.joinType {
	case JoinInner, JoinLeftSemi, JoinRightOuter:
		return true
	default:
		return false
	}
}

// FilterBatch 对一批探测行应用过滤器，返回放行行与统计。
// 未就绪时最多等待 waitTimeout；超时后本批原样放行，晚到的过滤器
// 只作用于其后的批。每一批只加载一份完整快照。
func (c *Coordinator) FilterBatch(rows []Row) ([]Row, BatchStats) {
	stats := BatchStats{Scanned: len(rows)}
	if !c.filterable() {
		stats.Passed = len(rows)
		stats.Reason = "连接类型为保留侧或未知，不过滤"
		c.logBatch(rows, rows, stats)
		return rows, stats
	}
	if !c.waitReady() {
		stats.Passed = len(rows)
		stats.Reason = "等待就绪超时，本批放行"
		c.logBatch(rows, rows, stats)
		return rows, stats
	}
	// 每批只加载一次快照，保证看到的是一份完整版本。
	f := c.current.Load()
	if f.Invalid {
		stats.Passed = len(rows)
		stats.Reason = "分片放弃，过滤器作废，全部放行"
		c.logBatch(rows, rows, stats)
		return rows, stats
	}
	kept := make([]Row, 0, len(rows))
	for _, r := range rows {
		if r.KeyNull {
			stats.Dropped++
			continue
		}
		if f.Match(r.Key) {
			kept = append(kept, r)
		} else {
			stats.Dropped++
		}
	}
	stats.Passed = len(kept)
	stats.Reason = "已就绪，按键摘要过滤"
	c.logBatch(rows, kept, stats)
	return kept, stats
}

// waitReady 等待过滤器定稿，超时返回 false。
func (c *Coordinator) waitReady() bool {
	select {
	case <-c.ready:
		return true
	default:
	}
	if c.waitTimeout <= 0 {
		return false
	}
	t := time.NewTimer(c.waitTimeout)
	defer t.Stop()
	select {
	case <-c.ready:
		return true
	case <-t.C:
		return false
	}
}

func (c *Coordinator) logBatch(in, out []Row, s BatchStats) {
	c.logger.Printf("batch join=%s scanned=%d passed=%d dropped=%d reason=%s in=%v out=%v",
		c.joinType, s.Scanned, s.Passed, s.Dropped, s.Reason, rowKeys(in), rowKeys(out))
}

func rowKeys(rows []Row) []any {
	keys := make([]any, len(rows))
	for i, r := range rows {
		if r.KeyNull {
			keys[i] = "NULL"
		} else {
			keys[i] = r.Key
		}
	}
	return keys
}
