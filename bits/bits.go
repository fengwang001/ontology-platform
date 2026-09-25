// Package bits 提供按大端序（每字节最高位在前）打包的位读取器与位写出器。
// 本包不依赖项目内其他包。
package bits

// Writer 把逐位写入的比特按大端序打包进字节。
type Writer struct {
	buf []byte
	cur byte // 当前字节已累积的位
	n   uint // cur 中有效位数，范围 0..7
}

// NewWriter 创建空的位写出器（空输入产出非 nil 的零长 []byte，即 []byte{}）。
func NewWriter() *Writer { return &Writer{buf: make([]byte, 0)} }

// WriteBit 写入一个比特（b 为 0 或 1）。
func (w *Writer) WriteBit(b int) {
	w.cur = w.cur<<1 | byte(b&1)
	w.n++
	if w.n == 8 {
		w.buf = append(w.buf, w.cur)
		w.cur, w.n = 0, 0
	}
}

// PadByte 把当前未填满字节的低位补 0 并落盘；已对齐时为空操作。
func (w *Writer) PadByte() {
	if w.n > 0 {
		w.buf = append(w.buf, w.cur<<(8-w.n))
		w.cur, w.n = 0, 0
	}
}

// Bytes 补齐末字节后返回已打包字节（返回内部切片，调用后不应继续写入）。
func (w *Writer) Bytes() []byte {
	w.PadByte()
	return w.buf
}

// Reader 按大端序从字节流逐位读取。
type Reader struct {
	buf        []byte
	pos        int  // 当前字节下标
	bit        uint // 字节内下一个待读位的下标，0..7（0 为最高位）
	lastChecks int  // 非导出：自上次 ResetCheckCount 起检查过的位数
}

// NewReader 基于给定字节创建位读取器（不拷贝，调用方不得在读取期间改写）。
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// ReadBit 返回下一个比特；比特耗尽时 ok 为 false。
func (r *Reader) ReadBit() (bit int, ok bool) {
	if r.pos >= len(r.buf) {
		return 0, false
	}
	r.lastChecks++
	b := int((r.buf[r.pos] >> (7 - r.bit)) & 1)
	r.bit++
	if r.bit == 8 {
		r.pos++
		r.bit = 0
	}
	return b, true
}

// ResetCheckCount 把「最近一次读码字检查的位数」计数清零，不读出数值。
// 应在解码每个新码字开始时调用；计数值仅供包内测试读取，不出现在公开读取接口。
func (r *Reader) ResetCheckCount() { r.lastChecks = 0 }

// StreamReader 是可增量喂入字节块的位读取器：游标在 Feed 之间保持，
// 因而同一比特流无论如何分块，读取顺序都与一次性读取一致。
type StreamReader struct {
	Reader
}

// NewStreamReader 创建空的流式位读取器。
func NewStreamReader() *StreamReader { return &StreamReader{} }

// Feed 追加一个字节块；已读到的位置不受影响，不会重扫前缀。
func (s *StreamReader) Feed(p []byte) { s.buf = append(s.buf, p...) }
