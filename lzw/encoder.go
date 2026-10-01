package lzw

import (
	"io"
	"sync"
)

// bitWriter 按低位在先把变长码打包进字节流。
type bitWriter struct {
	w   io.Writer
	acc uint32 // 未输出的位，始终低位对齐
	n   uint   // acc 中有效位数
	buf []byte
	err error
}

func newBitWriter(w io.Writer) *bitWriter {
	return &bitWriter{w: w, buf: make([]byte, 0, 256)}
}

func (b *bitWriter) writeCode(code int, bits uint) {
	if b.err != nil {
		return
	}
	b.acc |= uint32(code) << b.n
	b.n += bits
	for b.n >= 8 {
		b.buf = append(b.buf, byte(b.acc))
		b.acc >>= 8
		b.n -= 8
	}
	if len(b.buf) >= 128 {
		b.flush()
	}
}

func (b *bitWriter) flush() {
	if b.err != nil || len(b.buf) == 0 {
		return
	}
	n, err := b.w.Write(b.buf)
	if err == nil && n != len(b.buf) {
		err = io.ErrShortWrite
	}
	if err != nil {
		b.err = err
	}
	b.buf = b.buf[:0]
}

// finish 用 0 位补齐当前字节并刷出全部数据。
func (b *bitWriter) finish() error {
	if b.err == nil && b.n > 0 {
		b.buf = append(b.buf, byte(b.acc))
		b.acc = 0
		b.n = 0
	}
	b.flush()
	return b.err
}

// widthFor 返回 nextFree（下一个空闲编号）对应的输出码宽。
// 空闲编号达到 2^bits 之后输出的码使用 bits+1 位。
// 即：条目 e=2^bits 新增之后（free 变为 2^bits+1）才升宽。
func widthFor(nextFree int) uint {
	switch {
	case nextFree > 1<<11:
		return 12
	case nextFree > 1<<10:
		return 11
	case nextFree > 1<<9:
		return 10
	default:
		return 9
	}
}

type dictEntry struct {
	prefix int // 父串编号；单字节条目为 -1
	b      byte
}

// 条目结构在解码端用于按编号重建字节串。

// dictKey 用 (前缀编号, 字节) 拼出字典键；前缀编号 < 4096。
func dictKey(prefix int, b byte) uint64 {
	return uint64(uint32(prefix)<<8 | uint32(b))
}

// Encoder 是 GIF 变宽 LZW 流式编码器。
// 所有方法可并发调用，结果等价于某个串行顺序；输出字节只取决于写入的总
// 字节序列，与 Write 的切分方式无关。
type Encoder struct {
	mu     sync.Mutex
	bw     *bitWriter
	dict   map[uint64]int
	wCode  int // 当前串 w 的字典编号；-1 表示空串
	free   int // 下一个空闲编号
	closed bool
}

// NewEncoder 创建写入 w 的编码器；构造时立即按 9 位输出清除码。
func NewEncoder(w io.Writer) *Encoder {
	e := &Encoder{
		bw:    newBitWriter(w),
		dict:  make(map[uint64]int, 4096),
		wCode: -1,
		free:  firstFree,
	}
	e.bw.writeCode(ClearCode, minBits)
	return e
}

// reset 输出清除码（用当前码宽）并让字典回到只含 0..257、码宽回到 9。
// 当前串 w 由调用方保持不变。
func (e *Encoder) reset() {
	e.bw.writeCode(ClearCode, widthFor(e.free))
	e.dict = make(map[uint64]int, 4096)
	e.free = firstFree
}

func (e *Encoder) emit(code int) {
	e.bw.writeCode(code, widthFor(e.free))
}

// step 处理一个输入字节。
func (e *Encoder) step(c byte) {
	if e.wCode < 0 {
		// 空串 + 单字节恒在字典（即字面码 c）。
		e.wCode = int(c)
		return
	}
	key := dictKey(e.wCode, c)
	if id, ok := e.dict[key]; ok {
		e.wCode = id
		return
	}
	// w+c 不在字典：输出 w 的码，新增条目 w+c，w 置为 c。
	e.emit(e.wCode)
	id := e.free
	e.dict[key] = id
	e.free = id + 1
	if id == maxCode {
		// 新增 4095 后紧接输出清除码（当前码宽为 12），字典与码宽复位；
		// w 仍保持为 c。
		e.reset()
	}
	e.wCode = int(c)
}

// Write 写入待编码字节。
func (e *Encoder) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, ErrClosed
	}
	for i := range p {
		e.step(p[i])
		if e.bw.err != nil {
			return i, e.bw.err
		}
	}
	return len(p), nil
}

// Close 输出 w 的码（若非空）、补计下一个空闲编号并做加宽判定、
// 输出结束码，最后用 0 位补齐到整字节。
func (e *Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	e.closed = true
	if e.wCode >= 0 {
		e.emit(e.wCode)
		// 按正常流程把下一个空闲编号计为已占用并做同样的加宽判定：
		// 该编号不存在内容也不会被引用，只需推进 free 使结束码的码宽
		// 与解码端在处理完最后一个码后的码宽一致。
		if e.free <= maxCode {
			e.free++
			if e.free-1 == maxCode {
				// 写满后若仍要输出结束码，按规则先清除再输出。
				e.reset()
			}
		}
	}
	e.bw.writeCode(EndCode, widthFor(e.free))
	return e.bw.finish()
}
