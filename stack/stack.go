// Package stack 表示并规范化一条调用栈。
package stack

// Frame 是一个调用栈帧。空字符串 Name 合法。
type Frame struct {
	Name      string
	Truncated bool
}

// Stack 是根 → 叶的帧序列。
type Stack struct {
	Frames []Frame
}

// Normalize 折叠连续重复帧；深度超过 maxDepth 时保留前 maxDepth 帧，
// 并在最深保留帧上置 Truncated。空栈返回 ErrEmpty。
// maxDepth<=0 表示不限深度。
func Normalize(names []string, maxDepth int) (Stack, error) {
	if len(names) == 0 {
		return Stack{}, ErrEmpty
	}
	dedup := make([]string, 0, len(names))
	for _, n := range names {
		if len(dedup) > 0 && dedup[len(dedup)-1] == n {
			continue
		}
		dedup = append(dedup, n)
	}
	truncated := false
	if maxDepth > 0 && len(dedup) > maxDepth {
		dedup = dedup[:maxDepth]
		truncated = true
	}
	frames := make([]Frame, len(dedup))
	for i, n := range dedup {
		frames[i] = Frame{Name: n}
	}
	frames[len(frames)-1].Truncated = truncated
	return Stack{Frames: frames}, nil
}

// Depth 返回帧数。
func (s Stack) Depth() int { return len(s.Frames) }

// Truncated 报告该样本是否因深度上限被截断。
func (s Stack) Truncated() bool {
	return len(s.Frames) > 0 && s.Frames[len(s.Frames)-1].Truncated
}
