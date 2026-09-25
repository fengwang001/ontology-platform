// Package opool 实现对象池本体：Acquire/Release、块记账、哨兵错误、
// 复杂度计数器。依赖 blk，不依赖 api。
package opool

import (
	"errors"
	"sync"

	"ontology/blk"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrInvalidMaxIdle = errors.New("opool: maxIdle must be >= 1")
	ErrDoubleRelease  = errors.New("opool: block already idle")
	ErrUnknownBlock   = errors.New("opool: unknown or reclaimed block")
)

// Pool 是固定大小块的复用池，并发安全。
type Pool struct {
	mu      sync.Mutex
	maxIdle int
	free    blk.Stack
	live    map[*blk.Block]struct{} // 存活块（in-use + idle）
	nextID  int
	checked int // 最近一次 Acquire/Release 检查过的空闲块记录条数（非导出）
}

// New 创建池；maxIdle < 1 时整体失败，不产生任何状态。
func New(maxIdle int) (*Pool, error) {
	if maxIdle < 1 {
		return nil, ErrInvalidMaxIdle
	}
	return &Pool{maxIdle: maxIdle, live: make(map[*blk.Block]struct{})}, nil
}

// Acquire：空闲列表非空则弹栈顶复用（LIFO），否则新建。
func (p *Pool) Acquire() (*blk.Block, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checked = 0
	if p.free.Len() > 0 {
		p.checked++ // 只检查栈顶一条记录
		b := p.free.Pop()
		if err := b.MarkInUse(); err != nil {
			return nil, err
		}
		return b, nil
	}
	b := blk.New(p.nextID)
	p.nextID++
	p.live[b] = struct{}{}
	return b, nil
}

// Release：先校验后变更，任何拒绝都不留痕。
func (p *Pool) Release(b *blk.Block) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checked = 0
	if b == nil {
		return ErrUnknownBlock
	}
	if _, ok := p.live[b]; !ok { // 从未 Acquire 或已回收
		return ErrUnknownBlock
	}
	if b.State() == blk.Idle {
		return ErrDoubleRelease
	}
	if blk.Full(p.free.Len(), p.maxIdle) {
		if err := b.Reclaim(); err != nil {
			return err
		}
		delete(p.live, b) // 满池驱逐：不保留，总块数减一
		return nil
	}
	if err := b.MarkIdle(); err != nil {
		return err
	}
	p.free.Push(b)
	return nil
}

// Idle 返回当前空闲块数。
func (p *Pool) Idle() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.free.Len()
}

// Total 返回当前存活块总数（in-use + idle）。
func (p *Pool) Total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.live)
}
