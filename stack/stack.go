// Package stack 表示并规范化采样得到的调用栈。
package stack

// Stack 是一条规范化后的调用栈：Frames[0] 为最外层帧。
type Stack struct {
	Frames    []string
	Truncated bool // 原始栈深超过上限，末帧为截断点
}

// Valid 报告栈是否可用于建树（至少含一帧；空帧名本身合法）。
func (s Stack) Valid() bool { return len(s.Frames) > 0 }

// Normalize 对原始帧序列做规范化：
//  1. 折叠相邻的重复帧（递归自调用的同义噪音）；
//  2. 当 maxDepth>0 且折叠后帧数超过它时，保留前 maxDepth 帧并置截断标记；
//     帧数恰好等于上限不截断，上限+1 才截断。
//
// 空帧名 "" 是合法帧名；空序列返回 ok=false（无效样本）。
// 本函数不修改入参。
func Normalize(frames []string, maxDepth int) (s Stack, ok bool) {
	if len(frames) == 0 {
		return Stack{}, false
	}
	dedup := make([]string, 0, len(frames))
	for _, f := range frames {
		if len(dedup) > 0 && dedup[len(dedup)-1] == f {
			continue
		}
		dedup = append(dedup, f)
	}
	truncated := false
	if maxDepth > 0 && len(dedup) > maxDepth {
		dedup = dedup[:maxDepth]
		truncated = true
	}
	return Stack{Frames: dedup, Truncated: truncated}, true
}
