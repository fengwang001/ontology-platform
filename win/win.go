// Package win 维护可靠传输发送方的接收窗口右边缘记账：
// una/next/wnd 与派生的 right=una+wnd、avail=max(0,right-next)，
// 以及非零窗口收缩忽略、零窗口特例的判定。不依赖其他包。
package win

// Window 是发送方侧的窗口记账。right 不单独存储，恒等于 una+wnd，
// 从构造上保证「右边缘 = 已确认 + 通告窗口」这一不变量。
type Window struct {
	una  int64 // 累计已确认字节，单调不减
	next int64 // 下一发送字节序号，单调不减
	wnd  int64 // 最近通告的窗口大小
	// checked 记录最近一次 Send/RecvAck/RecvWindow 检查过的在途单元个数。
	// 非导出，不出现在任何公开接口；用于证明边界指针 O(1) 维护。
	checked int
}

// New 返回初始窗口：una=next=0，wnd=w0。
func New(w0 int64) Window { return Window{wnd: w0} }

// Una 返回累计已确认字节。
func (w *Window) Una() int64 { return w.una }

// Next 返回下一发送字节序号。
func (w *Window) Next() int64 { return w.next }

// Wnd 返回最近通告的窗口大小。
func (w *Window) Wnd() int64 { return w.wnd }

// Right 返回窗口右边缘 una+wnd。
func (w *Window) Right() int64 { return w.una + w.wnd }

// Avail 返回可发送空间 max(0, right-next)。
func (w *Window) Avail() int64 {
	if a := w.Right() - w.next; a > 0 {
		return a
	}
	return 0
}

// CanSend 判定 Send(n) 是否合法：n>0 且 n<=avail。只查边界指针，O(1)。
func (w *Window) CanSend(n int64) bool {
	w.checked = 1
	return n > 0 && n <= w.Avail()
}

// ApplySend 在 CanSend 通过后前进 next。
func (w *Window) ApplySend(n int64) { w.next += n }

// ValidAck 判定累积确认 a 是否合法：una <= a <= next。O(1)。
func (w *Window) ValidAck(a int64) bool {
	w.checked = 1
	return w.una <= a && a <= w.next
}

// ApplyAck 在 ValidAck 通过后前进 una（right 随之前进）。
func (w *Window) ApplyAck(a int64) { w.una = a }

// ValidWindow 判定通告窗口是否合法：nw >= 0。
func (w *Window) ValidWindow(nw int64) bool { return nw >= 0 }

// ApplyWindow 应用一次窗口通告：
//   - nw==0：零窗口，接受，wnd=0，right 收缩到 una（唯一合法收缩）；
//   - cand=una+nw >= right：正常/扩展，接受；
//   - cand < right：非零收缩，忽略，wnd 不变（右边缘不收缩）。
func (w *Window) ApplyWindow(nw int64) {
	w.checked = 1
	if nw == 0 {
		w.wnd = 0
		return
	}
	if w.una+nw >= w.Right() {
		w.wnd = nw
	}
}
