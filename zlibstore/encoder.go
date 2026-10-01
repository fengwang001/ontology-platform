package zlibstore

import (
	"fmt"
	"io"
	"sync"
)

// maxStoredLen 是单个 DEFLATE 存储块 LEN 字段允许的最大字节数。
const maxStoredLen = 65535

// zlibHeader 是本编码器固定输出的 zlib 头部：CM=8（deflate）、
// CINFO=0（32 字节窗口）、FCHECK 使 (CMF<<8|FLG) 被 31 整除、
// FDICT=0、FLEVEL=0，即字节 78 01。
var zlibHeader = [2]byte{0x78, 0x01}

// Encoder 是只用存储块的 zlib 容器流式编码器。
//
// 写入的数据缓存在内部，每攒满 65535 字节输出一个 BFINAL=0 的存储块；
// Close 时把剩余数据（允许 0 字节）作为 BFINAL=1 的终块输出，
// 随后追加 4 字节大端 Adler-32 尾部。因此输出只取决于写入的全部字节，
// 与 Write 的切分方式无关。
//
// Encoder 的所有方法可被并发调用，效果等价于某种串行顺序。
// Close 之后再 Write 或再 Close 返回 ErrClosed，且不改变任何状态与输出。
type Encoder struct {
	mu       sync.Mutex
	w        io.Writer
	buf      []byte // 已缓存但尚未成块的数据，长度始终 < maxStoredLen
	adler    adlerState
	started  bool  // 是否已写出 zlib 头
	closed   bool  // Close 已成功完成；之后所有调用被拒绝
	failed   bool  // 底层 writer 出错后的粘滞失败态
	writeErr error // 底层 writer 失败的原因
}

// NewEncoder 创建向 w 输出的编码器。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{
		w:     w,
		buf:   make([]byte, 0, maxStoredLen),
		adler: newAdler(),
	}
}

// Write 缓存数据并在满 65535 字节时刷出非终存储块。
func (e *Encoder) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch {
	case e.closed:
		return 0, ErrClosed
	case e.failed:
		return 0, fmt.Errorf("%w: %v", ErrWriter, e.writeErr)
	}

	if !e.started {
		if err := e.emitHeader(); err != nil {
			e.markFailed(err)
			return 0, err
		}
	}

	consumed := 0
	for len(p) > 0 {
		// 上一轮结束时满块必已刷出，因此 len(buf) < maxStoredLen，
		// 这里总有空间接收至少 1 个字节。注意 copy 的目标必须是
		// buf 的可写尾部，不能跨越 cap 边界。
		room := maxStoredLen - len(e.buf)
		take := min(len(p), room)
		e.buf = append(e.buf, p[:take]...)
		n := take
		p = p[n:]
		consumed += n

		if len(e.buf) == maxStoredLen {
			if err := e.flushBlock(finalBitNo); err != nil {
				e.markFailed(err)
				return consumed, err
			}
		}
	}
	return consumed, nil
}

// Close 输出终块与 Adler-32 尾部；重复调用一律返回 ErrClosed。
func (e *Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch {
	case e.closed:
		return ErrClosed
	case e.failed:
		return fmt.Errorf("%w: %v", ErrWriter, e.writeErr)
	}

	if !e.started {
		if err := e.emitHeader(); err != nil {
			e.markFailed(err)
			return err
		}
	}

	if err := e.flushBlock(finalBitYes); err != nil {
		e.markFailed(err)
		return err
	}

	var trailer [4]byte
	sum := e.adler.sum()
	trailer[0] = byte(sum >> 24)
	trailer[1] = byte(sum >> 16)
	trailer[2] = byte(sum >> 8)
	trailer[3] = byte(sum)
	if err := e.rawWrite(trailer[:]); err != nil {
		e.markFailed(err)
		return err
	}

	e.closed = true
	return nil
}

const (
	finalBitNo  = 0
	finalBitYes = 1
)

// emitHeader 写出固定的 2 字节 zlib 头 78 01。
func (e *Encoder) emitHeader() error {
	if err := e.rawWrite(zlibHeader[:]); err != nil {
		return err
	}
	e.started = true
	return nil
}

// flushBlock 把 e.buf 整体作为一个存储块输出，并清空缓存。
// 存储块布局：1 字节块头（仅最低位 BFINAL 可能为 1）、
// 2 字节小端 LEN、2 字节小端 NLEN（LEN 低 16 位的按位取反）、数据。
func (e *Encoder) flushBlock(final int) error {
	length := len(e.buf)

	var header [5]byte
	header[0] = byte(final & 1)
	header[1] = byte(length)
	header[2] = byte(length >> 8)
	nlen := ^uint16(length)
	header[3] = byte(nlen)
	header[4] = byte(nlen >> 8)
	if err := e.rawWrite(header[:]); err != nil {
		return err
	}

	// Adler-32 与块输出同步推进：即使数据写出失败，状态也即将进入
	// 粘滞失败态，不会再产生任何输出。
	data := e.buf
	e.adler.update(data)
	if err := e.rawWrite(data); err != nil {
		return err
	}

	e.buf = e.buf[:0]
	return nil
}

// rawWrite 要求底层 writer 一次吃完全部字节（io.Writer 契约允许短写，
// 短写同样视为致命错误，因为容器字节流已无法保持一致）。
func (e *Encoder) rawWrite(p []byte) error {
	n, err := e.w.Write(p)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWriter, err)
	}
	if n != len(p) {
		return fmt.Errorf("%w: short write %d/%d", ErrWriter, n, len(p))
	}
	return nil
}

// writeErr 保存底层 writer 的原始错误以便后续调用暴露同一原因。
// markFailed 进入粘滞失败态；failed 优先于 closed，拒绝后续一切调用。
func (e *Encoder) markFailed(err error) {
	e.failed = true
	e.writeErr = err
}
