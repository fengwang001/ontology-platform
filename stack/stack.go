// Package stack 表示并规范化一条采样调用栈。
package stack

// DefaultMaxDepth 是未显式指定时采用的最大栈深。
const DefaultMaxDepth = 64

// Frame 是调用栈中的一帧；空函数名是合法的。
type Frame struct {
	Func      string
	File      string
	Line      int
	Truncated bool // 该帧之后的内容因超过深度上限被截断
}

// equal 判断两帧是否同一调用点。
func equal(a, b Frame) bool {
	return a.Func == b.Func && a.File == b.File && a.Line == b.Line
}

// Result 是规范化结果。
type Result struct {
	Frames     []Frame
	Truncated  bool // 是否发生了深度截断
	Dropped    int  // 因截断而丢弃的帧数
	Invalid    bool // 输入为空栈
	DedupedAdj int  // 合并掉的完全相同相邻帧数量
}

// Normalize 复制并规范化一条调用栈：
// 先合并完全相同的相邻帧（去重），再按 maxDepth 做尾部截断。
// 空栈返回 Invalid；maxDepth<=0 时使用 DefaultMaxDepth。
func Normalize(frames []Frame, maxDepth int) Result {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	if len(frames) == 0 {
		return Result{Invalid: true}
	}

	out := make([]Frame, 0, len(frames))
	deduped := 0
	for _, f := range frames {
		f.Truncated = false
		if len(out) > 0 && equal(out[len(out)-1], f) {
			deduped++
			continue
		}
		out = append(out, f)
	}

	res := Result{Frames: out, DedupedAdj: deduped}
	if len(out) > maxDepth {
		res.Dropped = len(out) - maxDepth
		res.Frames = out[:maxDepth]
		res.Frames[maxDepth-1].Truncated = true
		res.Truncated = true
	}
	return res
}
