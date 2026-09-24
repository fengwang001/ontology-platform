// Package source 提供可注入的数据源：可配置产出速率、中途报错、提前结束。
package source

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrInjected 是按配置注入的中途错误。
var ErrInjected = errors.New("source: injected error")

// Raw 是数据源吐出的一条原始字节消息，Off 为单调序号（字节偏移语义）。
type Raw struct {
	Off  int64
	Data []byte
}

// Source 是可恢复的数据源。Seek 到 off 后，Next 从 off 开始产出。
type Source interface {
	Seek(off int64)
	Next(ctx context.Context) (Raw, error)
}

// Config 配置内存数据源。
type Config struct {
	Rate  time.Duration // 每条产出前的延迟，0 表示全速
	ErrAt int           // 产出 ErrAt 条之后下一次 Next 返回 ErrInjected，<=0 表示不注入
	Limit int           // 提前结束：最多产出多少条，0 表示全部 Items
}

// Mem 是基于内存切片的数据源，崩溃后用相同 Items 重建即可重放。
type Mem struct {
	items [][]byte
	cfg   Config
	pos   int
}

// NewMem 创建内存数据源。
func NewMem(items [][]byte, cfg Config) *Mem {
	if cfg.Limit <= 0 || cfg.Limit > len(items) {
		cfg.Limit = len(items)
	}
	return &Mem{items: items, cfg: cfg}
}

// Seek 实现 Source，off 之前的记录将被跳过。
func (m *Mem) Seek(off int64) {
	m.pos = int(off)
}

// Next 实现 Source，正常结束返回 io.EOF。
func (m *Mem) Next(ctx context.Context) (Raw, error) {
	if m.cfg.Rate > 0 {
		select {
		case <-time.After(m.cfg.Rate):
		case <-ctx.Done():
			return Raw{}, ctx.Err()
		}
	}
	if m.cfg.ErrAt > 0 && m.pos >= m.cfg.ErrAt {
		return Raw{}, ErrInjected
	}
	if m.pos >= m.cfg.Limit {
		return Raw{}, io.EOF
	}
	r := Raw{Off: int64(m.pos), Data: m.items[m.pos]}
	m.pos++
	return r, nil
}
