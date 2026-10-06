package grading

import "fmt"

// gridRound 将平均分子（tick 的两倍）向满分方向靠拢到相邻网格点。
// twice 表示 2*ticks；奇数时 ceil（向高分方向靠拢），结果夹在 [0,maxTicks]。
func gridRound(twice int, maxTicks int) int {
	r := twice / 2
	if twice%2 != 0 {
		r++ // 半个网格点：向满分方向取相邻网格点
	}
	if r > maxTicks {
		return maxTicks
	}
	if r < 0 {
		return 0
	}
	return r
}

// absTick 返回两个网格分的差的绝对值。
func absTick(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// Kind 对错误进行分类。数值越大优先级越高，用于冲突时确定唯一错误归因。
type Kind int

const (
	KindOK Kind = iota
	KindInvalidParam
	KindNotFound
	KindInactive
	KindTaskState
	KindScore
	KindNoCandidate
)

// Error 携带分类、优先级与可读信息。
type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(k Kind, format string, args ...any) error {
	return &Error{Kind: k, Message: fmt.Sprintf(format, args...)}
}
