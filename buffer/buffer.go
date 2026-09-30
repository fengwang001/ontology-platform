// Package buffer 提供带背压的批量冲刷变更缓冲。
//
// 缓冲先进先出，按批量大小与最旧条目停留时长两档触发冲刷，
// 批量触发优先；达到高水位时新的写入整体拒收；一次冲刷若下游
// 报错，整批按原顺序放回缓冲头部并立即停止，时钟回滚到冲刷前。
package buffer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// 可区分的失败原因。
var (
	// ErrInvalidConfig 表示批量大小、高水位或延迟阈值不合法。
	ErrInvalidConfig = errors.New("buffer: invalid config")
	// ErrBackpressure 表示缓冲达到高水位，写入被整体拒收。
	ErrBackpressure = errors.New("buffer: backpressure, buffer full")
	// ErrEmptyKey 表示写入的键为空。
	ErrEmptyKey = errors.New("buffer: empty key")
)

// FlushError 表示一次冲刷因下游报错而整体回滚。
type FlushError struct {
	// Trigger 是本次冲刷的触发类型。
	Trigger Trigger
	// Batch 是尝试冲刷的条目数。
	Batch int
	// Cause 是下游返回的原始错误。
	Cause error
}

func (e *FlushError) Error() string {
	return fmt.Sprintf("buffer: flush failed (trigger=%s, batch=%d): %v", e.Trigger, e.Batch, e.Cause)
}

func (e *FlushError) Unwrap() error { return e.Cause }

// Trigger 表示冲刷触发类型。
type Trigger int

const (
	// TriggerNone 表示未触发。
	TriggerNone Trigger = iota
	// TriggerBatch 表示按批量大小触发（优先档）。
	TriggerBatch
	// TriggerDelay 表示按最旧条目停留时长触发。
	TriggerDelay
)

func (t Trigger) String() string {
	switch t {
	case TriggerBatch:
		return "batch"
	case TriggerDelay:
		return "delay"
	default:
		return "none"
	}
}

// Entry 是一条变更条目。
type Entry struct {
	Key   string
	Value string
	// Seq 是写入被接受时分配的单调递增序号，从 1 开始。
	Seq uint64
	// EnqueuedAt 是条目进入缓冲的时刻（来自注入时钟）。
	EnqueuedAt time.Time
}

// Sink 是下游接口。Flush 必须原子生效：要么整批应用成功，
// 要么返回错误且不产生任何部分效果。
type Sink interface {
	Flush(ctx context.Context, entries []Entry) error
}

// SinkFunc 把函数适配为 Sink。
type SinkFunc func(ctx context.Context, entries []Entry) error

// Flush 实现 Sink。
func (f SinkFunc) Flush(ctx context.Context, entries []Entry) error {
	return f(ctx, entries)
}

// Clock 提供当前时刻，便于测试注入手动时钟。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Config 是缓冲配置。
type Config struct {
	// BatchSize 是批量触发阈值，必须 >= 1。
	BatchSize int
	// HighWatermark 是高水位，必须 >= BatchSize。
	HighWatermark int
	// MaxDelay 是最旧条目停留时长阈值，必须 > 0。
	MaxDelay time.Duration
	// Clock 可选，默认真实时钟。
	Clock Clock
}

func (c Config) validate() error {
	if c.BatchSize < 1 {
		return fmt.Errorf("%w: batch size must be >= 1, got %d", ErrInvalidConfig, c.BatchSize)
	}
	if c.HighWatermark < c.BatchSize {
		return fmt.Errorf("%w: high watermark must be >= batch size %d, got %d", ErrInvalidConfig, c.BatchSize, c.HighWatermark)
	}
	if c.MaxDelay <= 0 {
		return fmt.Errorf("%w: max delay must be > 0, got %s", ErrInvalidConfig, c.MaxDelay)
	}
	return nil
}

// Stats 是缓冲的瞬时状态快照。
type Stats struct {
	Buffered      int
	Accepted      uint64
	Flushed       uint64
	Rejected      uint64
	FlushAttempts uint64
	FlushFailures uint64
	// LastTrigger 是最近一次冲刷的触发类型。
	LastTrigger Trigger
	// Clock 是逻辑时钟：等于已接受写入总数，失败冲刷会回滚到冲刷前。
	Clock uint64
}

// Buffer 是带背压的批量冲刷变更缓冲，可并发使用。
type Buffer struct {
	cfg  Config
	sink Sink
	now  func() time.Time

	mu      sync.Mutex
	queue   []Entry // FIFO，头部为最旧条目
	seq     uint64  // 逻辑时钟：已接受写入总数
	flushed uint64  // 已成功冲刷总数
	stats   Stats
}

// New 创建缓冲。配置不合法时返回 ErrInvalidConfig。
func New(cfg Config, sink Sink) (*Buffer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if sink == nil {
		return nil, fmt.Errorf("%w: sink must not be nil", ErrInvalidConfig)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	cfg.Clock = clock
	return &Buffer{cfg: cfg, sink: sink, now: clock.Now}, nil
}

// Write 接收一条变更写入。
//
// 键为空返回 ErrEmptyKey；缓冲达到高水位时整体拒收并返回
// ErrBackpressure，条目不会进入缓冲。写入被接受后依次检查批量
// 触发（优先）与延迟触发，命中即同步冲刷；下游报错时整批按原
// 顺序放回缓冲头部并返回 *FlushError，缓冲内容、顺序、下游已收
// 总量与逻辑时钟均保持不变。
func (b *Buffer) Write(ctx context.Context, key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.queue) >= b.cfg.HighWatermark {
		b.stats.Rejected++
		return ErrBackpressure
	}

	b.seq++
	b.queue = append(b.queue, Entry{
		Key:        key,
		Value:      value,
		Seq:        b.seq,
		EnqueuedAt: b.now(),
	})
	b.stats.Accepted++

	return b.maybeFlushLocked(ctx)
}

// FlushIfDue 主动检查两档触发条件（批量优先），命中即冲刷。
// 用于延迟触发：调用方（或定时器）推进时钟后调用本方法。
// 未触发返回 nil。
func (b *Buffer) FlushIfDue(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maybeFlushLocked(ctx)
}

// maybeFlushLocked 判定触发类型并冲刷，调用时必须持有锁。
func (b *Buffer) maybeFlushLocked(ctx context.Context) error {
	trigger := b.triggerLocked()
	if trigger == TriggerNone {
		return nil
	}
	return b.flushLocked(ctx, trigger)
}

// triggerLocked 返回当前触发类型，批量触发优先于延迟触发。
func (b *Buffer) triggerLocked() Trigger {
	if len(b.queue) >= b.cfg.BatchSize {
		return TriggerBatch
	}
	if len(b.queue) > 0 && !b.now().Before(b.queue[0].EnqueuedAt.Add(b.cfg.MaxDelay)) {
		return TriggerDelay
	}
	return TriggerNone
}

// flushLocked 弹出至多一个批量交给下游；下游报错时整批按原顺序
// 放回缓冲头部并立即停止，逻辑时钟回滚到本次冲刷前。
func (b *Buffer) flushLocked(ctx context.Context, trigger Trigger) error {
	n := b.cfg.BatchSize
	if len(b.queue) < n {
		n = len(b.queue)
	}
	batch := make([]Entry, n)
	copy(batch, b.queue[:n])
	b.queue = b.queue[n:]

	clockBefore := b.seq
	b.stats.FlushAttempts++
	b.stats.LastTrigger = trigger

	if err := b.sink.Flush(ctx, batch); err != nil {
		// 整体回滚：整批按原顺序放回头部，时钟回滚，立即停止。
		restored := make([]Entry, 0, len(batch)+len(b.queue))
		restored = append(restored, batch...)
		restored = append(restored, b.queue...)
		b.queue = restored
		b.seq = clockBefore
		b.stats.FlushFailures++
		return &FlushError{Trigger: trigger, Batch: n, Cause: err}
	}

	b.flushed += uint64(n)
	return nil
}

// Stats 返回瞬时状态快照，可并发调用。
func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats
	s.Buffered = len(b.queue)
	s.Flushed = b.flushed
	s.Clock = b.seq
	return s
}

// Snapshot 返回缓冲内条目的有序副本（FIFO 顺序），可并发调用。
func (b *Buffer) Snapshot() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Entry, len(b.queue))
	copy(out, b.queue)
	return out
}

// SelfCheck 校验不变量，可并发调用：
//
//  1. 已冲刷数 + 缓冲内数 == 已接受数（不丢不重）；
//  2. 缓冲内条目序号恰好是 flushed+1 .. accepted 的连续递增序列
//     （顺序可复现，下游已收恰为 1..flushed 的前缀）；
//  3. 缓冲内条数不超过高水位。
func (b *Buffer) SelfCheck() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.flushed+uint64(len(b.queue)) != b.seq {
		return fmt.Errorf("buffer: self-check failed: flushed(%d)+buffered(%d) != accepted(%d)",
			b.flushed, len(b.queue), b.seq)
	}
	for i, e := range b.queue {
		want := b.flushed + uint64(i) + 1
		if e.Seq != want {
			return fmt.Errorf("buffer: self-check failed: queue[%d].Seq=%d, want %d", i, e.Seq, want)
		}
	}
	if len(b.queue) > b.cfg.HighWatermark {
		return fmt.Errorf("buffer: self-check failed: buffered(%d) exceeds high watermark(%d)",
			len(b.queue), b.cfg.HighWatermark)
	}
	return nil
}
