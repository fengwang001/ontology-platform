// Package window 实现固定容量的滑动窗口（环形缓冲），
// 支持按距离取回历史字节。本包不依赖其他包。
package window

// Window 是固定容量的字节环形缓冲，逻辑下标 0 为最旧字节。
type Window struct {
	buf  []byte
	head int // 最旧字节的物理下标（仅在满后有意义）
	size int // 当前有效字节数
}

// New 创建容量为 capacity 的窗口；capacity 必须为正，否则 panic。
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Cap 返回窗口容量。
func (w *Window) Cap() int { return len(w.buf) }

// Len 返回当前有效字节数。
func (w *Window) Len() int { return w.size }

// Append 追加一个字节；窗口满时挤掉最旧字节。
func (w *Window) Append(b byte) {
	if w.size < len(w.buf) {
		w.buf[(w.head+w.size)%len(w.buf)] = b
		w.size++
		return
	}
	w.buf[w.head] = b
	w.head = (w.head + 1) % len(w.buf)
}

// AppendBytes 逐字节追加 p。
func (w *Window) AppendBytes(p []byte) {
	for _, b := range p {
		w.Append(b)
	}
}

// Get 按距离取回历史字节：dist=1 是最近写入的字节。调用方保证 1 ≤ dist ≤ Len()。
func (w *Window) Get(dist int) byte {
	return w.buf[(w.head+w.size-dist)%len(w.buf)]
}

// Copy 把以距离 dist 结尾、长度为 n 的历史字节按正向顺序拷到 dst 前 n 字节。
// 调用方保证 dist+n-1 ≤ Len()；重叠回指须配合追加分多轮调用（见 dec）。
func (w *Window) Copy(dist, n int, dst []byte) {
	start := (w.head + w.size - dist - n + 1) % len(w.buf)
	k := len(w.buf) - start
	if k > n {
		k = n
	}
	copy(dst, w.buf[start:start+k])
	copy(dst[k:n], w.buf[:n-k])
}
