// Package blk 实现等大小不透明块的状态机与空闲块 LIFO 栈。
// 不依赖本工程其他包。
package blk

import "errors"

// State 是块的唯一状态。
type State int

const (
	InUse     State = iota // 已 Acquire 未 Release
	Idle                   // 在空闲列表中
	Reclaimed              // 已被满池驱逐回收
)

// ErrBadTransition 表示非法状态迁移。
var ErrBadTransition = errors.New("blk: invalid state transition")

// Block 是等大小的不透明对象，状态唯一。
type Block struct {
	id int
	st State
}

// New 新建一个 in-use 状态的块。
func New(id int) *Block { return &Block{id: id, st: InUse} }

// ID 返回块编号（仅用于展示与测试比对）。
func (b *Block) ID() int { return b.id }

// State 返回块当前状态。
func (b *Block) State() State { return b.st }

// MarkIdle：in-use -> idle。
func (b *Block) MarkIdle() error {
	if b.st != InUse {
		return ErrBadTransition
	}
	b.st = Idle
	return nil
}

// MarkInUse：idle -> in-use。
func (b *Block) MarkInUse() error {
	if b.st != Idle {
		return ErrBadTransition
	}
	b.st = InUse
	return nil
}

// Reclaim：in-use -> reclaimed（满池驱逐）。
func (b *Block) Reclaim() error {
	if b.st != InUse {
		return ErrBadTransition
	}
	b.st = Reclaimed
	return nil
}

// Stack 是空闲块的 LIFO 栈，只动栈顶，O(1)。
type Stack struct {
	items []*Block
}

// Len 返回空闲块数。
func (s *Stack) Len() int { return len(s.items) }

// Push 把块压入栈顶。
func (s *Stack) Push(b *Block) { s.items = append(s.items, b) }

// Pop 弹出栈顶；空栈返回 nil。
func (s *Stack) Pop() *Block {
	n := len(s.items)
	if n == 0 {
		return nil
	}
	b := s.items[n-1]
	s.items[n-1] = nil
	s.items = s.items[:n-1]
	return b
}

// Full 判定空闲数是否已达上限（满池驱逐条件）。
func Full(idle, maxIdle int) bool { return idle >= maxIdle }
