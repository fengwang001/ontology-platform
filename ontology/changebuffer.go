// Package ontology 提供本体服务平台的核心组件。
package ontology

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// 可区分的失败原因，调用方用 errors.Is 判定。
var (
	// ErrInvalidConfig 批量大小、高水位或延迟阈值不合法。
	ErrInvalidConfig = errors.New("changebuffer: invalid config")
	// ErrBackpressure 缓冲达到高水位，写入被整体拒收。
	ErrBackpressure = errors.New("changebuffer: backpressure: buffer at high water mark")
	// ErrEmptyKey 写入包含空键，整批拒绝。
	ErrEmptyKey = errors.New("changebuffer: empty key")
	// ErrDownstream 下游冲刷失败，整批已回滚。
	ErrDownstream = errors.New("changebuffer: downstream flush failed, batch rolled back")
)

// Change 是一条待冲刷的变更条目。
type Change struct {
	Key   string
	Value any
}

// Sink 是下游消费者，ApplyBatch 必须原子地应用整批：
// 返回错误时缓冲假定整批均未生效并整体回滚。
type Sink interface {
	ApplyBatch(ctx context.Context, batch []Change) error
}

// SinkFunc 把函数适配为 Sink。
type SinkFunc func(ctx context.Context, batch []Change) error

// ApplyBatch 实现 Sink。
func (f SinkFunc) ApplyBatch(ctx context.Context, batch []Change) error {
	return f(ctx, batch)
}

// Trigger 标识一次冲刷的触发类型。
type Trigger int

const (
	// TriggerNone 未触发。
	TriggerNone Trigger = iota
	// TriggerBatch 缓冲条目数达到批量大小触发（优先级高）。
	TriggerBatch
	// TriggerLatency 最旧条目停留时长达到延迟阈值触发。
	TriggerLatency
)

func (t Trigger) String() string {
	switch t {
	case TriggerBatch:
		return "batch"
	case TriggerLatency:
		return "latency"
	default:
		return "none"
	}
}

// Config 是 ChangeBuffer 的配置。
type Config struct {
	// BatchSize 批量触发阈值，必须 >= 1。
	BatchSize int
	// HighWater 高水位（缓冲容量上限），必须 >= BatchSize。
	HighWater int
	// MaxDelay 延迟触发阈值（最旧条目最大停留时长），必须 > 0。
	MaxDelay time.Duration
	// Clock 可注入时钟，nil 时用真实时钟；测试可注入假时钟保证可复现。
	Clock func() time.Time
}

func (c Config) validate() error {
	if c.BatchSize < 1 {
		return fmt.Errorf("%w: batch size %d must be >= 1", ErrInvalidConfig, c.BatchSize)
	}
	if c.HighWater < c.BatchSize {
		return fmt.Errorf("%w: high water %d must be >= batch size %d", ErrInvalidConfig, c.HighWater, c.BatchSize)
	}
	if c.MaxDelay <= 0 {
		return fmt.Errorf("%w: max delay %s must be > 0", ErrInvalidConfig, c.MaxDelay)
	}
	return nil
}

// Stats 是缓冲的只读快照，供并发查询。
type Stats struct {
	Len       int
	HighWater int
	Accepted  uint64
	Rejected  uint64
	Flushed   uint64
	Rolled    uint64
}

// ChangeBuffer 是带背压的批量冲刷变更缓冲。
// 所有方法均可并发调用。
type ChangeBuffer struct {
	mu        sync.Mutex
	queue     []Change
	oldestAt  time.Time
	batchSize int
	highWater int
	maxDelay  time.Duration
	clock     func() time.Time
	sink      Sink
	accepted  uint64
	rejected  uint64
	flushed   uint64
	rolled    uint64
	// lastTrigger 记录最近一次冲刷的触发类型，供测试与自检观测。
	lastTrigger Trigger
}

// NewChangeBuffer 校验配置并创建缓冲；配置非法时返回 ErrInvalidConfig。
func NewChangeBuffer(cfg Config, sink Sink) (*ChangeBuffer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if sink == nil {
		return nil, fmt.Errorf("%w: sink must not be nil", ErrInvalidConfig)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &ChangeBuffer{
		batchSize: cfg.BatchSize,
		highWater: cfg.HighWater,
		maxDelay:  cfg.MaxDelay,
		clock:     clock,
		sink:      sink,
	}, nil
}

// Write 原子地写入一批变更：
// 任一条目键为空则整批拒绝（ErrEmptyKey）；
// 写入后会超过高水位则整批拒收（ErrBackpressure），不入缓冲；
// 全部接受后按“批量优先、延迟其次”评估是否触发冲刷。
func (b *ChangeBuffer) Write(ctx context.Context, changes ...Change) error {
	if len(changes) == 0 {
		return nil
	}
	for _, c := range changes {
		if c.Key == "" {
			return ErrEmptyKey
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.queue)+len(changes) > b.highWater {
		b.rejected += uint64(len(changes))
		return fmt.Errorf("%w: need %d, have %d/%d",
			ErrBackpressure, len(changes), len(b.queue), b.highWater)
	}
	if len(b.queue) == 0 {
		b.oldestAt = b.clock()
	}
	b.queue = append(b.queue, changes...)
	b.accepted += uint64(len(changes))
	_, err := b.evaluateLocked(ctx)
	return err
}

// evaluateLocked 按“批量优先、延迟其次”评估触发并冲刷，调用方须持锁。
// 冲刷成功后若仍满足触发条件则继续，下游报错立即停止。
func (b *ChangeBuffer) evaluateLocked(ctx context.Context) (Trigger, error) {
	first := TriggerNone
	for {
		trigger := TriggerNone
		switch {
		case len(b.queue) >= b.batchSize:
			trigger = TriggerBatch
		case len(b.queue) > 0 && !b.clock().Before(b.oldestAt.Add(b.maxDelay)):
			trigger = TriggerLatency
		}
		if trigger == TriggerNone {
			return first, nil
		}
		if first == TriggerNone {
			first = trigger
		}
		b.lastTrigger = trigger
		if err := b.flushLocked(ctx); err != nil {
			return first, err
		}
	}
}

// flushLocked 把队首至多 batchSize 条按 FIFO 顺序交给下游；
// 下游报错时整批按原顺序放回队头、时钟（oldestAt）回滚到冲刷前并立即停止。
// 调用方须持锁。
func (b *ChangeBuffer) flushLocked(ctx context.Context) error {
	n := b.batchSize
	if len(b.queue) < n {
		n = len(b.queue)
	}
	if n == 0 {
		return nil
	}
	batch := make([]Change, n)
	copy(batch, b.queue[:n])
	prevOldest := b.oldestAt
	if err := b.sink.ApplyBatch(ctx, batch); err != nil {
		// 整批回滚：队列、顺序、下游已收总量与时钟均不变。
		b.oldestAt = prevOldest
		b.rolled += uint64(n)
		return fmt.Errorf("%w: %v", ErrDownstream, err)
	}
	b.queue = b.queue[n:]
	if len(b.queue) == 0 {
		b.oldestAt = time.Time{}
	}
	b.flushed += uint64(n)
	return nil
}

// Check 是自检：按当前时钟评估延迟触发，到期则冲刷。
// 查询与自检可并发调用，互不影响。
func (b *ChangeBuffer) Check(ctx context.Context) (Trigger, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.evaluateLocked(ctx)
}

// Stats 返回缓冲的只读统计快照。
func (b *ChangeBuffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Stats{
		Len:       len(b.queue),
		HighWater: b.highWater,
		Accepted:  b.accepted,
		Rejected:  b.rejected,
		Flushed:   b.flushed,
		Rolled:    b.rolled,
	}
}

// Snapshot 返回当前缓冲内容的副本（FIFO 顺序），不修改缓冲。
func (b *ChangeBuffer) Snapshot() []Change {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Change, len(b.queue))
	copy(out, b.queue)
	return out
}
