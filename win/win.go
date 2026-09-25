// Package win 维护接收窗口右边缘记账：una/next/wnd/right、
// 收缩判定、零窗口特例与 avail 计算。不依赖其他包。
package win

// Window 是窗口右边缘记账器。所有字段非导出，外部只能经方法访问。
type Window struct {
	una     int64 // 累计已确认字节，单调不减
	next    int64 // 下一发送字节序号，单调不减
	wnd     int64 // 最近通告的窗口大小
	right   int64 // 窗口右边缘 = una + wnd
	checked int64 // 最近一次操作检查过的在途单元个数（O(1) 证明用）
}

// New 返回初始窗口：una=next=0，wnd=right=w0。
func New(w0 int64) *Window {
	return &Window{wnd: w0, right: w0}
}

func (w *Window) Una() int64   { return w.una }
func (w *Window) Next() int64  { return w.next }
func (w *Window) Wnd() int64   { return w.wnd }
func (w *Window) Right() int64 { return w.right }

// Avail 返回可发送空间，恒 >= 0。
func (w *Window) Avail() int64 {
	if a := w.right - w.next; a > 0 {
		return a
	}
	return 0
}

// Sendable 判定 n 是否可发送：n>0 且 n<=avail。只查边界，O(1)。
func (w *Window) Sendable(n int64) bool {
	w.checked = 1
	return n > 0 && n <= w.Avail()
}

// AdvanceSend 落地一次发送：next += n。
func (w *Window) AdvanceSend(n int64) { w.next += n }

// AckValid 判定累积确认 a 是否合法：una <= a <= next。O(1)。
func (w *Window) AckValid(a int64) bool {
	w.checked = 1
	return w.una <= a && a <= w.next
}

// ApplyAck 落地确认：una=a，右边缘随确认前进。
func (w *Window) ApplyAck(a int64) {
	w.una = a
	w.right = w.una + w.wnd
}

// WindowValid 判定通告窗口是否合法：v >= 0。O(1)。
func (w *Window) WindowValid(v int64) bool {
	w.checked = 1
	return v >= 0
}

// ApplyWindow 落地窗口通告：零窗口接受并收缩 right 到 una（唯一合法
// 收缩）；非零且 cand>=right 正常更新；非零且 cand<right 为收缩，忽略。
func (w *Window) ApplyWindow(v int64) {
	if v == 0 {
		w.wnd = 0
		w.right = w.una
		return
	}
	if cand := w.una + v; cand >= w.right {
		w.wnd = v
		w.right = cand
	}
}
