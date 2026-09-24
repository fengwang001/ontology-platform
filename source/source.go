// Package source 提供可注入的流式数据源。
package source

import (
	"context"
	"errors"
	"io"
	"time"
)

// Frame 是 source 产出的一帧原始数据。Pos 为单调递增的帧序号。
type Frame struct {
	Pos  int64
	Data []byte
}

// Source 是数据源抽象。Next 返回 io.EOF 表示正常结束。
type Source interface {
	Next(ctx context.Context) (Frame, error)
}

// ErrEnd 由 Gen 配置触发，模拟 source 提前结束。
var ErrEnd = errors.New("source: injected early end")

// GenConfig 配置可编程数据源。
type GenConfig struct {
	N        int           // 计划产出帧数
	Rate     time.Duration // 每帧间隔，0 表示全速
	ErrAt    int           // 在产出第 ErrAt 帧（从 0 计）前返回 ErrAtVal；<0 关闭
	ErrAtVal error         // 中途返回的错误，默认为 ErrEnd
	EndAfter int           // >=0 时只产出 EndAfter 帧后返回 io.EOF
	BadEvery int           // >0 时每隔 BadEvery 帧产出一帧坏数据
}

// Gen 是可编程内存数据源，从 StartAt 位置开始重放，供恢复使用。
type Gen struct {
	cfg     GenConfig
	nextPos int64
}

// NewGen 从 startAt 帧开始产出。
func NewGen(cfg GenConfig, startAt int64) *Gen {
	if cfg.ErrAt >= 0 && cfg.ErrAtVal == nil {
		cfg.ErrAtVal = ErrEnd
	}
	if cfg.EndAfter == 0 {
		cfg.EndAfter = -1
	}
	return &Gen{cfg: cfg, nextPos: startAt}
}

// Next 返回下一帧。
func (g *Gen) Next(ctx context.Context) (Frame, error) {
	if g.cfg.Rate > 0 {
		select {
		case <-time.After(g.cfg.Rate):
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		}
	}
	if g.cfg.ErrAt >= 0 && g.nextPos == int64(g.cfg.ErrAt) {
		return Frame{}, g.cfg.ErrAtVal
	}
	if g.cfg.EndAfter >= 0 && g.nextPos >= int64(g.cfg.EndAfter) {
		return Frame{}, io.EOF
	}
	if g.nextPos >= int64(g.cfg.N) {
		return Frame{}, io.EOF
	}
	data := encode(g.nextPos, g.cfg.BadEvery)
	f := Frame{Pos: g.nextPos, Data: data}
	g.nextPos++
	return f, nil
}

func encode(pos int64, badEvery int) []byte {
	if badEvery > 0 && pos%int64(badEvery) == int64(badEvery-1) {
		return []byte("broken-frame")
	}
	key := string(rune('a' + pos%5))
	return []byte(key + "=1")
}
