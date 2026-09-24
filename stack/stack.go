// Package stack 表示调用栈并提供规范化：帧序列、去重、最大深度截断。
package stack

import "errors"

// DefaultMaxDepth 是未指定上限时使用的最大栈深。
const DefaultMaxDepth = 64

// ErrEmpty 表示采样得到空栈，调用方应拒绝并计入无效样本。
var ErrEmpty = errors.New("stack: empty stack")

// Stack 是一条规范化后的调用栈，Frames[0] 为根帧，末元素为栈顶（叶）帧。
// 帧名允许为空串。
type Stack struct {
	Frames    []string
	Truncated bool // 因超过最大深度被截断
}

// Normalize 校验并规范化帧序列：空栈返回 ErrEmpty；深度超过 maxDepth 时
// 保留前 maxDepth 帧并置 Truncated 标记，不丢弃整个样本。
// maxDepth <= 0 时使用 DefaultMaxDepth。深度恰好等于上限不截断。
func Normalize(frames []string, maxDepth int) (Stack, error) {
	if len(frames) == 0 {
		return Stack{}, ErrEmpty
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	s := Stack{Frames: frames}
	if len(frames) > maxDepth {
		s.Frames = frames[:maxDepth]
		s.Truncated = true
	}
	return s, nil
}

// Depth 返回栈深。
func (s Stack) Depth() int { return len(s.Frames) }

// Leaf 返回栈顶（叶）帧名。
func (s Stack) Leaf() string { return s.Frames[len(s.Frames)-1] }

// Dedup 返回折叠了连续重复帧的新栈（如 A→B→B→C 变为 A→B→C）。
// 它只折叠相邻重复，保留跨层级的同名帧，因此递归结构（A→F→G→F）不受影响。
// 注意：规范化默认不做去重，以免丢失直接递归（F→F）的层级信息。
func Dedup(frames []string) []string {
	out := frames[:0]
	for i, f := range frames {
		if i == 0 || f != frames[i-1] {
			out = append(out, f)
		}
	}
	return out
}
