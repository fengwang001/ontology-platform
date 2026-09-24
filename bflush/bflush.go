// Package bflush 持有 buf.Buffer 与下游 Sink，维护逻辑时钟，
// 实现 Tick 排水循环、失败整体回滚与 Flush/FlushAll。依赖方向：bflush -> buf。
package bflush

import (
	"errors"
	"sync"

	"ontology/buf"
)

// Sink 是下游；一次 flush 恰好调用 Apply 一次。
type Sink interface {
	Apply(batch []buf.Entry) error
}

// 四类互不相同的可判定哨兵错误。
var (
	ErrInvalidParam = errors.New("bflush: invalid parameter")
	ErrHighWater    = errors.New("bflush: buffer at high watermark")
	ErrEmptyKey     = errors.New("bflush: empty key")
	ErrSink         = errors.New("bflush: sink apply failed")
)

// Engine 是带背压的批量 flush 变更缓冲核心；并发安全。
type Engine struct {
	mu        sync.Mutex
	q         *buf.Buffer
	sink      Sink
	b         int
	now       int64
	delivered int64
}

// New 按 B/H/L 与 Sink 构造引擎并校验参数（B>=1、H>=B、L>=1）。
func New(b, h, l int, sink Sink) (*Engine, error) {
	if b < 1 || h < b || l < 1 || sink == nil {
		return nil, ErrInvalidParam
	}
	return &Engine{q: buf.New(b, h, l), sink: sink, b: b}, nil
}

// Write 接收一条写；空 key 或高水位时拒收且状态不变（先校验后改状态）。
func (e *Engine) Write(key string, val int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	if e.q.Full() {
		return ErrHighWater
	}
	e.q.Push(key, val, e.now)
	return nil
}

// applyOnce 取出头部一批交给 Sink；失败则整批按原顺序放回，调用方据此回滚时钟。
func (e *Engine) applyOnce(batch []buf.Entry, lease *buf.Lease) error {
	if err := e.sink.Apply(batch); err != nil {
		e.q.Return(lease)
		return ErrSink
	}
	e.delivered += int64(len(batch))
	return nil
}

// Tick 推进逻辑时钟一格并进入排水循环；批量优先、循环到两触发皆不成立。
// 任一批失败立即停止：该批已整体回滚，时钟还原到本 Tick 之前。
func (e *Engine) Tick() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.now
	e.now++
	for {
		batch, lease, kind := e.q.TakeTick(e.now)
		if kind == buf.None {
			break
		}
		if err := e.applyOnce(batch, lease); err != nil {
			e.now = old
			return int(old), err
		}
	}
	return int(e.now), nil
}

// Flush 强制 flush 头部一批（至多 B 条），返回本批条数；失败整体回滚（时钟不动）。
func (e *Engine) Flush() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	batch, lease, fired := e.q.TakeFlush()
	if !fired {
		return 0, nil
	}
	if err := e.applyOnce(batch, lease); err != nil {
		return 0, err
	}
	return len(batch), nil
}

// FlushAll 反复强制 flush 直到缓冲为空；任一批失败立即停止并整体回滚。
func (e *Engine) FlushAll() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for {
		batch, lease, fired := e.q.TakeFlush()
		if !fired {
			return nil
		}
		if err := e.applyOnce(batch, lease); err != nil {
			return err
		}
	}
}

// Buffered 返回当前缓冲条目数。
func (e *Engine) Buffered() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.q.Len()
}

// Delivered 返回 Sink 已成功收到的条目总量（单调不减）。
func (e *Engine) Delivered() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.delivered
}
