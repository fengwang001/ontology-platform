// Package mono 单遍单调扫描：对每个位置求右侧第一个严格更大元素的下标。
package mono

import (
	"errors"
	"sync/atomic"

	"ontology/stack"
)

// None 表示右侧不存在满足条件的元素；它与任何合法下标（含 0）都不混淆。
const None = -1

// 三类可判定且互不相同的哨兵错误。
var (
	ErrNilInput = errors.New("mono: nil input slice")
	ErrTooLong  = errors.New("mono: input length exceeds configured limit")
	ErrBadLimit = errors.New("mono: limit must be positive")
)

// Scanner 维护可配置的长度上限与压弹计数器；只读扫描可并发调用。
type Scanner struct {
	maxLen int
	ops    atomic.Int64 // 压栈与弹栈的总次数，非导出，不进公开接口
}

// NewScanner 创建扫描器；上限非正时拒绝并返回 ErrBadLimit。
func NewScanner(maxLen int) (*Scanner, error) {
	if maxLen <= 0 {
		return nil, ErrBadLimit
	}
	return &Scanner{maxLen: maxLen}, nil
}

// Scan 单遍扫描 a，返回每个位置右侧第一个严格更大元素的下标，不存在为 None。
// 任何校验失败都在计算前返回，不修改输入、不返回半截结果。
func (s *Scanner) Scan(a []int) ([]int, error) {
	if a == nil {
		return nil, ErrNilInput
	}
	if len(a) > s.maxLen {
		return nil, ErrTooLong
	}
	ans := make([]int, len(a))
	for i := range ans {
		ans[i] = None
	}
	var st stack.Stack
	for i := range a {
		s.step(&st, a, i, ans)
	}
	return ans, nil
}

// step 处理 a[i]：弹出所有值小于 a[i] 的栈顶并为其定答案（相等不弹，
// 即「严格更大」语义），随后压入 i。循环不变量：栈内值自底向顶非递增。
func (s *Scanner) step(st *stack.Stack, a []int, i int, ans []int) {
	for {
		top, ok := st.Top()
		if !ok || a[top] >= a[i] {
			break
		}
		_, _ = st.Pop()
		s.ops.Add(1)
		ans[top] = i
	}
	st.Push(i)
	s.ops.Add(1)
}
