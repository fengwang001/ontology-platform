// Package bits 提供大端比特流的写出器与读取器（字节游标 + 位游标）。
// 它不依赖工程内任何其他包。
package bits

import "io"

// Writer 把比特按从高位到低位的顺序打包进字节；末字节未用位保持 0。
type Writer struct {
	buf   []byte
	fill  uint // 当前末字节已写入的位数，范围 0..7
	nbits uint64
}

// NewWriter 创建一个空的位写出器。
func NewWriter() *Writer { return &Writer{} }

// WriteBit 写入单个比特（b 的最低位）。
func (w *Writer) WriteBit(b byte) {
	if w.fill == 0 {
		w.buf = append(w.buf, 0)
	}
	if b&1 == 1 {
		w.buf[len(w.buf)-1] |= 1 << (7 - w.fill)
	}
	w.fill = (w.fill + 1) & 7
	w.nbits++
}

// WriteBits 写入 v 的低 n 位（n 位为一个大端比特串），n 必须 <= 64。
func (w *Writer) WriteBits(v uint64, n uint) {
	for i := int(n) - 1; i >= 0; i-- {
		w.WriteBit(byte((v >> uint(i)) & 1))
	}
}

// Bytes 返回已打包的字节（末字节低位补 0）。
func (w *Writer) Bytes() []byte { return w.buf }

// BitLen 返回已写入的比特总数。
func (w *Writer) BitLen() uint64 { return w.nbits }

// Reader 按比特读取一段字节，pos 是全局位游标。
type Reader struct {
	data []byte
	pos  uint64
	// codeStart/lastCodeBits 是非导出的单码打点：lastCodeBits 记录最近一次
	// 单码读取自 StartCode 起实际检查过的位数。该计数器不出现在任何对外
	// 接口中，仅工程内部及测试可见。
	codeStart    uint64
	lastCodeBits uint64
}

// NewReader 基于给定字节创建位读取器（不拷贝、不修改输入）。
func NewReader(data []byte) *Reader { return &Reader{data: data} }

// ReadBit 读取下一个比特；比特耗尽时返回 io.EOF。
func (r *Reader) ReadBit() (byte, error) {
	if r.pos >= uint64(len(r.data))*8 {
		return 0, io.EOF
	}
	b := (r.data[r.pos/8] >> (7 - r.pos%8)) & 1
	r.pos++
	r.lastCodeBits = r.pos - r.codeStart
	return byte(b), nil
}

// ReadBits 读取接下来的 n 位并作为 uint64 返回（n <= 64）；中途耗尽返回 io.EOF。
func (r *Reader) ReadBits(n uint) (uint64, error) {
	var v uint64
	for i := uint(0); i < n; i++ {
		b, err := r.ReadBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | uint64(b)
	}
	return v, nil
}

// Pos 返回当前位游标（已消费的比特数）。
func (r *Reader) Pos() uint64 { return r.pos }

// StartCode 标记一个单码读取的起点；此后 lastCodeBits 随读取自动更新。
func (r *Reader) StartCode() { r.codeStart, r.lastCodeBits = r.pos, 0 }

// TailIsZeroPadding 报告从当前游标到字节结尾的剩余位是否全为 0
// （无剩余位也算），不移动游标。用于识别末字节的 0 填充。
func (r *Reader) TailIsZeroPadding() bool {
	for p := r.pos; p < uint64(len(r.data))*8; p++ {
		if (r.data[p/8]>>(7-p%8))&1 == 1 {
			return false
		}
	}
	return true
}
