// Package source 提供可注入的确定性内存数据源。
package source

import (
	"context"
	"errors"
	"io"
	"time"
)

// Record 是 source 产出的原始记录：偏移 + 一行字节。
type Record struct {
	Off int64
	Raw []byte
}

// IsBarrier 判断是否为屏障标记（Raw=nil）。
func (r Record) IsBarrier() bool { return r.Raw == nil }

// Item 是 parse 之后在阶段间流动的单位；Barrier 为屏障标记。
type Item struct {
	Off     int64
	Key     string
	Val     int64
	barrier bool
}

// Barrier 构造偏移为 off 的屏障。
func Barrier(off int64) Item { return Item{Off: off, barrier: true} }

// IsBarrier 判断该 Item 是否为屏障。
func (it Item) IsBarrier() bool { return it.barrier }

// Source 读取原始记录。Read 返回 io.EOF 表示正常结束。
type Source interface {
	Read(ctx context.Context) (Record, error)
}

// Config 配置内存源。
type Config struct {
	Total      int           // 正常记录总数（最大偏移）
	StartAfter int64         // 从该偏移之后开始产出（恢复用）
	BadEvery   int           // 每隔多少条插一条坏记录；0 不插
	ErrorAfter int           // 产出多少条后注入错误；0 不报错
	EarlyEnd   bool          // true 时注入错误语义为提前结束
	Delay      time.Duration // 每条产出延迟（模拟速率限制）
}

// ErrInjected 是注入式 source 错误。
var ErrInjected = errors.New("source: injected error")

// NewMem 创建内存源。第 n 条正常行为 "kX=n"（X=n%7）；坏行没有 '='。
func NewMem(cfg Config) Source {
	return &memSource{cfg: cfg, next: cfg.StartAfter}
}

type memSource struct {
	cfg  Config
	next int64
}

// Line 生成确定性的第 n 条记录内容，供 source 与测试共用。
func Line(n int64) []byte {
	return []byte("k" + itoa(n%7) + "=" + itoa(n))
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (m *memSource) Read(ctx context.Context) (Record, error) {
	if m.cfg.Delay > 0 {
		select {
		case <-ctx.Done():
			return Record{}, ctx.Err()
		case <-time.After(m.cfg.Delay):
		}
	}
	m.next++
	off := m.next
	if m.cfg.ErrorAfter > 0 && int(off) > m.cfg.ErrorAfter {
		if m.cfg.EarlyEnd {
			return Record{}, errors.New("source: early end")
		}
		return Record{}, ErrInjected
	}
	if off > int64(m.cfg.Total) {
		return Record{}, io.EOF
	}
	raw := Line(off)
	if m.cfg.BadEvery > 0 && int(off)%m.cfg.BadEvery == 0 {
		raw = []byte("bad-row-no-separator")
	}
	return Record{Off: off, Raw: raw}, nil
}
