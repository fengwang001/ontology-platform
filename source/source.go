// Package source 提供可注入的内存数据源。
package source

import (
	"errors"
	"time"
)

// Item 是 source 产出的一个元素。Data 与 Barrier 互斥。
type Item struct {
	Offset  int64
	Data    []byte
	Barrier int64 // >0 表示屏障事件，值为屏障 offset
}

// Source 是一次性数据源。
type Source interface {
	// Next 返回下一个元素；ok=false 表示正常结束。
	Next() (item Item, ok bool, err error)
	// Skip 在恢复时丢弃前 n 条数据（不含屏障）。
	Skip(n int64)
}

// Resumable 表示支持恢复定位的 source（管线恢复时使用）。
type Resumable interface {
	Source
	SeekTo(offset int64)
}

// Option 配置 Mem source。
type Option func(*Mem)

// WithInterval 设置每条数据产出间隔（0 表示全速）。
func WithInterval(d time.Duration) Option {
	return func(m *Mem) { m.interval = d }
}

// WithErrorAt 在第 n 条数据（1 基）返回 err，模拟中途报错。
func WithErrorAt(n int64, err error) Option {
	return func(m *Mem) { m.errAt = n; m.err = err }
}

// WithEndAfter 只产出前 n 条数据，模拟提前结束。
func WithEndAfter(n int64) Option {
	return func(m *Mem) { m.endAfter = n }
}

// Mem 是基于字节行切片的内存数据源，可重放。
type Mem struct {
	rows     [][]byte
	pos      int64 // 已产出数据条数
	barrier  int64 // 屏障间隔；0 表示由调用方自行注入
	interval time.Duration
	errAt    int64
	endAfter int64
	err      error
	slept    int64
	blocked  int64 // 由外部（管线）通过 NoteBlock 记录
}

// NewMem 创建数据源。barrierEvery>0 时每产出该条数自动附一个屏障事件。
func NewMem(rows [][]byte, barrierEvery int64, opts ...Option) *Mem {
	m := &Mem{rows: rows, barrier: barrierEvery}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Seek 实现 Resumable：从第 offset 条数据之后继续。
func (m *Mem) SeekTo(offset int64) { m.pos = offset }

// Skip 实现 Source：恢复时跳过 n 条。
func (m *Mem) Skip(n int64) {
	if n > m.pos {
		m.pos = n
	}
}

// Blocked 返回产出过程中因下游背压被阻塞的次数（由管线记录）。
func (m *Mem) Blocked() int64 { return m.blocked }

// NoteBlock 记录一次上游阻塞。
func (m *Mem) NoteBlock() { m.blocked++ }

// Next 实现 Source。
func (m *Mem) Next() (Item, bool, error) {
	if m.errAt > 0 && m.pos+1 == m.errAt {
		return Item{}, false, m.err
	}
	if m.pos >= int64(len(m.rows)) || (m.endAfter > 0 && m.pos >= m.endAfter) {
		return Item{}, false, nil
	}
	if m.interval > 0 {
		time.Sleep(m.interval)
	}
	off := m.pos + 1
	row := m.rows[m.pos]
	m.pos = off
	item := Item{Offset: off, Data: row}
	return item, true, nil
}

// BarrierEvery 返回屏障间隔。
func (m *Mem) BarrierEvery() int64 { return m.barrier }

// ErrSentinel 是可用于注入测试的哨兵错误。
var ErrSentinel = errors.New("source injected error")
