// Package window 按序号暂存乱序结果的重排窗口：只放行下一个该输出的
// 序号，并通过 Admit 把暂存数限制在窗口上限内。不依赖其他包。
package window

// Window 是单 goroutine 使用的重排窗口，非并发安全。
type Window struct {
	lim  int         // 暂存数上限 W
	next int         // 下一个该输出的序号
	buf  map[int]any // 暂存的乱序结果
	max  int         // 历史最大暂存数
}

// New 创建上限为 lim 的窗口。
func New(lim int) *Window { return &Window{lim: lim, buf: make(map[int]any)} }

// Admit 报告序号 i 是否落在当前窗口内；遵守它可保证暂存数不超过 lim。
func (w *Window) Admit(i int) bool { return i-w.next < w.lim }

// Put 暂存序号 i 的结果，并更新历史最大暂存数。
func (w *Window) Put(i int, v any) {
	w.buf[i] = v
	if len(w.buf) > w.max {
		w.max = len(w.buf)
	}
}

// Ready 报告下一个该输出的序号是否已有结果。
func (w *Window) Ready() bool {
	_, ok := w.buf[w.next]
	return ok
}

// Pop 取出下一个该输出的结果并推进序号，需先确认 Ready。
func (w *Window) Pop() any {
	v := w.buf[w.next]
	delete(w.buf, w.next)
	w.next++
	return v
}

// MaxHeld 返回历史最大暂存数。
func (w *Window) MaxHeld() int { return w.max }
