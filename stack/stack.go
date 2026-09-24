// Package stack 表示并规范化采样得到的调用栈（根→叶的帧序列）。
package stack

import "errors"

// ErrEmpty 表示传入了空栈；空栈不是合法样本。
var ErrEmpty = errors.New("stack: empty stack")

// Frame 是一帧调用。Name 允许为空串（合法帧名）。
type Frame struct {
	Name string
}

// Stack 是一条规范化后的调用路径：Frames[0] 为最外层（根）。
// Truncated 为 true 表示原始栈深超过上限，末尾帧为截断点。
type Stack struct {
	Frames    []Frame
	Truncated bool
}

// Normalize 把原始帧序列规范化为 Stack：
//   - 拒绝空栈（返回 ErrEmpty）；
//   - 合并相邻连续重复帧（递归展开造成的自调用去重）；
//   - maxDepth > 0 且去重后深度超过上限时，保留前 maxDepth 帧并置截断标记。
//
// 空串帧名是合法帧。maxDepth <= 0 表示不限制深度。
func Normalize(names []string, maxDepth int) (Stack, error) {
	if len(names) == 0 {
		return Stack{}, ErrEmpty
	}
	dedup := make([]Frame, 0, len(names))
	for _, name := range names {
		if len(dedup) > 0 && dedup[len(dedup)-1].Name == name {
			continue
		}
		dedup = append(dedup, Frame{Name: name})
	}
	s := Stack{Frames: dedup}
	if maxDepth > 0 && len(dedup) > maxDepth {
		s.Frames = dedup[:maxDepth]
		s.Truncated = true
	}
	return s, nil
}

// Depth 返回规范化后的栈深。
func (s Stack) Depth() int { return len(s.Frames) }

// Leaf 返回终止帧名。
func (s Stack) Leaf() string {
	if len(s.Frames) == 0 {
		return ""
	}
	return s.Frames[len(s.Frames)-1].Name
}
