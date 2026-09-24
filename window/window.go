// Package window 是固定容量的环形字节缓冲，保存最近的历史字节。
package window

// Window 非并发安全。容量固定，超出后最旧字节被覆盖。
type Window struct {
	buf  []byte
	head int // 最旧字节在 buf 中的下标
	size int // 逻辑长度
}

func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

func (w *Window) Cap() int { return len(w.buf) }
func (w *Window) Len() int { return w.size }

// At 取逻辑下标 i（0 为最旧）的字节。
func (w *Window) At(i int) byte {
	return w.buf[(w.head+i)%len(w.buf)]
}

// Write 追加字节，超出容量时丢弃最旧字节。
func (w *Window) Write(p []byte) {
	c := len(w.buf)
	if len(p) >= c {
		copy(w.buf, p[len(p)-c:])
		w.head, w.size = 0, c
		return
	}
	start := (w.head + w.size) % c
	if w.size == c {
		start = w.head
	}
	n := copy(w.buf[start:], p)
	if n < len(p) {
		copy(w.buf, p[n:])
}
	if w.size < c {
		w.size += len(p)
		if w.size > c {
			w.size = c
		}
	}
	w.head = (w.head + len(p)) % c
}

// CopyExpand 从距末尾 dist 的历史位置向前复制 length 字节到 dst。
// 允许 length > dist：每次至多复制 dist 字节，后续源即含新展开字节，
// 语义等价于逐字节向前复制，用于重叠回指的解压。
func (w *Window) CopyExpand(dst []byte, dist, length int) []byte {
	startIdx := w.size - dist
	for length > 0 {
		first := w.size - startIdx // 已可读取的连续历史长度
		if first > dist {
			first = dist
		}
		if first > length {
			first = length
		}
		for j := 0; j < first; j++ {
			dst = append(dst, w.At(startIdx+j))
		}
		// 将本轮新字节落窗，使下一轮能读到它们。
		added := dst[len(dst)-first:]
		w.Write(added)
		startIdx += first
		length -= first
	}
	return dst
}
