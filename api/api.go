// Package api 是无锁栈对外的稳定门面：New/Push/Pop/Len/Close/SelfCheck。
// 依赖方向单向：api -> s -> node，不允许反向依赖。
package api

import "ontology/s"

// Stack 并发安全：Push/Pop/Len/SelfCheck 可被多个 goroutine 同时调用。
type Stack struct {
	inner *s.Stack
}

// New 创建容量为 maxLen 的空栈；maxLen < 1 返回 ErrInvalidMaxLen。
func New(maxLen int) (*Stack, error) {
	inner, err := s.New(maxLen)
	if err != nil {
		return nil, err
	}
	return &Stack{inner: inner}, nil
}

// Push 压栈；满返回 ErrFull，Close 之后返回 ErrClosed；失败不改状态。
func (st *Stack) Push(v int) error { return st.inner.Push(v) }

// Pop 严格弹出栈顶（LIFO）；空或关闭且已弹空时返回 (0,false)。
func (st *Stack) Pop() (int, bool) { return st.inner.Pop() }

// Len 等于已入栈数减已出栈数，原子读取 O(1)，恒在 [0,maxLen]。
func (st *Stack) Len() int { return st.inner.Len() }

// Close 置关闭标志；重复关闭返回 ErrClosed。关闭后 Pop 先弹空剩余元素。
func (st *Stack) Close() error { return st.inner.Close() }

// SelfCheck 用内置操作序列核验四条不变量，全过返回 nil；可并发调用。
func (st *Stack) SelfCheck() error { return st.inner.SelfCheck() }

// 哨兵错误直接复用 s 层定义，保证 errors.Is 全链路可判定且两两不同。
var (
	ErrInvalidMaxLen = s.ErrInvalidMaxLen
	ErrFull          = s.ErrFull
	ErrClosed        = s.ErrClosed
)
