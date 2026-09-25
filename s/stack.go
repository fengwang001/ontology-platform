// Package s 实现 Treiber 无锁栈：单链表 + 单个栈顶指针 CAS。
// 依赖方向：s -> node，不反向依赖。
package s

import (
	"errors"
	"sync/atomic"

	"ontology/node"
)

// 三个哨兵错误两两不同，调用方用 errors.Is 可判定。
var (
	ErrInvalidMaxLen = errors.New("s: maxLen must be >= 1")
	ErrFull          = errors.New("s: stack is full")
	ErrClosed        = errors.New("s: stack is closed")
)

// Stack 是无锁栈。零值不可用，必须经 New 构造。
type Stack struct {
	top    atomic.Pointer[node.Node]
	maxLen int64

	// pushed 是「已预订入栈位」的计数：Push 先 CAS 占坑再发布节点，
	// 保证 Len=pushed-popped 恒不超过 maxLen，且 Len 为 O(1)。
	pushed atomic.Int64
	popped atomic.Int64
	closed atomic.Bool

	// lastPopVisited 记录最近一次 Pop 访问过的节点个数（非导出，
	// 仅允许 s 包内测试读取；不经过任何导出接口暴露其数值）。
	lastPopVisited atomic.Int64
}

// New 创建容量为 maxLen 的空栈；maxLen < 1 返回 ErrInvalidMaxLen。
func New(maxLen int) (*Stack, error) {
	if maxLen < 1 {
		return nil, ErrInvalidMaxLen
	}
	return &Stack{maxLen: int64(maxLen)}, nil
}

// Push 把新节点 CAS 到栈顶。满返回 ErrFull，关闭后返回 ErrClosed；
// 两者都在任何状态写入之前返回，失败不留痕。
func (s *Stack) Push(v int) error {
	// 第一步：原子占坑。占坑成功才发布节点，杜绝两个 Push 同时
	// 看到 Len==maxLen-1 而双双越界。
	for {
		if s.closed.Load() {
			return ErrClosed
		}
		p := s.pushed.Load()
		if p-s.popped.Load() >= s.maxLen {
			return ErrFull
		}
		if s.pushed.CompareAndSwap(p, p+1) {
			break
		}
	}
	// 第二步：发布。新节点指向当前栈顶，CAS 栈顶；失败只重试发布，
	// 坑位已占，不会重复计数。
	for {
		old := s.top.Load()
		nd := node.New(v, old)
		if s.top.CompareAndSwap(old, nd) {
			return nil
		}
	}
}

// Pop 把栈顶 CAS 到它的后继：读栈顶→读后继→CAS 是同一次 CAS 尝试，
// 任何并发 Push/Pop 改动栈顶都会使本次 CAS 失败并重试，绝不覆盖。
func (s *Stack) Pop() (int, bool) {
	visited := int64(0)
	for {
		old := s.top.Load()
		if old == nil {
			s.lastPopVisited.Store(visited) // 空栈：本轮未访问任何节点
			return 0, false
		}
		visited++ // 本次尝试检查了栈顶这一个节点
		next := old.Next()
		if s.top.CompareAndSwap(old, next) {
			s.popped.Add(1)
			s.lastPopVisited.Store(visited)
			return old.V(), true
		}
	}
}

// Len 等于已入栈数减已出栈数，原子读取，O(1)。
func (s *Stack) Len() int { return int(s.pushed.Load() - s.popped.Load()) }

// Close 置关闭标志；重复关闭返回 ErrClosed（可判定）。
// 关闭后 Push 永久报 ErrClosed，Pop 继续把剩余元素弹空。
func (s *Stack) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return ErrClosed
	}
	return nil
}
