// Package source 提供可注入的字节数据源。
package source

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrEnded 表示数据源按配置提前结束（非故障）。
var ErrEnded = errors.New("source: ended early by config")

// ErrInjected 表示按配置注入的 source 中途错误。
var ErrInjected = errors.New("source: injected failure")

// Source 是可恢复的数据源。Position 单调，Seek 用于崩溃后从检查点重放。
type Source interface {
	// Next 返回下一条原始记录及其消费位置（位置从 1 开始）。
	Next(ctx context.Context) (pos int64, data []byte, err error)
	SeekTo(pos int64)
}

// MemSource 是进程内存中的确定性数据源，可配置速率、中途报错与提前结束。
// Blocks 记录「想产出但被下游背压阻塞」的次数（由调用方在阻塞前后回调）。
type MemSource struct {
	total   int
	groups  int
	start   int64 // 当前游标（下一条的位置，从 1 起）
	delay   time.Duration
	errAt   int // 产出到第几条时返回错误；0 表示不报错
	endAt   int // 产出到第几条时提前正常结束；0 表示不提前结束
	errText string
	blocks  int
}

// Config 配置 MemSource。
type Config struct {
	Total       int
	Groups      int
	Delay       time.Duration
	ErrorAt     int
	EndEarlyAt  int
	ErrorText   string
}

// New 创建内存数据源。
func New(c Config) *MemSource {
	groups := c.Groups
	if groups < 1 {
		groups = 1
	}
	return &MemSource{
		total:   c.Total,
		groups:  groups,
		delay:   c.Delay,
		errAt:   c.ErrorAt,
		endAt:   c.EndEarlyAt,
		errText: c.ErrorText,
	}
}

// Seek 实现 Source。
func (s *MemSource) SeekTo(pos int64) {
	if pos < 0 {
		pos = 0
	}
	s.start = pos
}

// Position 返回当前游标（已读出的条数）。
func (s *MemSource) Position() int64 { return s.start }

// Blocks 返回历史阻塞次数。
func (s *MemSource) Blocks() int { return s.blocks }

// IncBlock 在产出被下游阻塞时由调用方调用。
func (s *MemSource) IncBlock() { s.blocks++ }

// Next 实现 Source。
func (s *MemSource) Next(ctx context.Context) (int64, []byte, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
	idx := int(s.start)
	if s.endAt > 0 && idx >= s.endAt {
		return 0, nil, ErrEnded
	}
	if idx >= s.total {
		return 0, nil, nil
	}
	if s.errAt > 0 && idx == s.errAt {
		text := s.errText
		if text == "" {
			text = "injected source error"
		}
		return 0, nil, fmt.Errorf("%s at %d: %w", text, idx, ErrInjected)
	}
	key := idx % s.groups
	line := fmt.Sprintf("k%d\t1\n", key)
	s.start++
	return s.start, []byte(line), nil
}
