// Package stack 表示并规范化采样得到的调用栈。
package stack

// Frame 是一个调用栈帧，帧名为空串也是合法帧。
type Frame struct {
	Name string
}

// Stack 是从栈底（调用发起方）到栈顶（当前函数）的帧序列。
type Stack struct {
	Frames    []Frame
	Truncated bool // 规范化时是否因超过最大深度而截断
}

// Normalize 复制并规范化栈：
//   - 相邻同名帧去重（同一帧被连续记录两次，按一次计）；
//   - 深度超过 maxDepth 时保留前 maxDepth 帧并置 Truncated；
//   - 空名帧合法，保留；空输入返回空栈。
func Normalize(frames []Frame, maxDepth int) Stack {
	var dedup []Frame
	for _, f := range frames {
		if len(dedup) > 0 && dedup[len(dedup)-1].Name == f.Name {
			continue
		}
		dedup = append(dedup, f)
	}
	out := Stack{Frames: dedup}
	if maxDepth > 0 && len(out.Frames) > maxDepth {
		out.Frames = out.Frames[:maxDepth]
		out.Truncated = true
	}
	return out
}

// FromNames 用函数名快速构造栈（栈底在前）。
func FromNames(names ...string) Stack {
	frames := make([]Frame, len(names))
	for i, n := range names {
		frames[i] = Frame{Name: n}
	}
	return Stack{Frames: frames}
}

// Len 返回帧数量。
func (s Stack) Len() int { return len(s.Frames) }

// Empty 报告栈是否不含任何帧。
func (s Stack) Empty() bool { return len(s.Frames) == 0 }

// Valid 报告栈是否可用于插入树（至少一帧）。
func (s Stack) Valid() bool { return len(s.Frames) > 0 }

// Names 返回帧名切片（栈底在前），供展示使用。
func (s Stack) Names() []string {
	names := make([]string, len(s.Frames))
	for i, f := range s.Frames {
		names[i] = f.Name
	}
	return names
}

// Equal 比较两个规范化后的栈是否完全一致（含截断标记）。
func (s Stack) Equal(other Stack) bool {
	if s.Truncated != other.Truncated || len(s.Frames) != len(other.Frames) {
		return false
	}
	for i := range s.Frames {
		if s.Frames[i].Name != other.Frames[i].Name {
			return false
		}
	}
	return true
}
