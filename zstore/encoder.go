package zstore

import "sync"

// MaxBlockLen 为单个存储块的最大数据长度。
const MaxBlockLen = 65535

// zlibHeader 为编码器固定输出的 2 字节头。
var zlibHeader = []byte{0x78, 0x01}

// Encoder 是只含存储块的 zlib 容器流式编码器。
// 并发安全：多个 goroutine 同时调用等价于某个串行顺序。
type Encoder struct {
	mu     sync.Mutex
	out    []byte
	buf    []byte
	sum    adler32
	closed bool
}

// NewEncoder 返回一个已写入固定头 78 01 的编码器。
func NewEncoder() *Encoder {
	e := &Encoder{sum: newAdler32()}
	e.out = append(e.out, zlibHeader...)
	return e
}

// Write 缓存数据，每满 65535 字节输出一个非终块。
func (e *Encoder) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, ErrClosed
	}
	e.sum.Write(p)
	e.buf = append(e.buf, p...)
	for len(e.buf) >= MaxBlockLen {
		e.emitBlock(false, e.buf[:MaxBlockLen])
		copy(e.buf, e.buf[MaxBlockLen:])
		e.buf = e.buf[:len(e.buf)-MaxBlockLen]
	}
	return len(p), nil
}

// Close 把剩余数据（可为 0 字节）作为终块输出，再输出 Adler-32 尾部。
func (e *Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	e.closed = true
	e.emitBlock(true, e.buf)
	e.buf = nil
	s := e.sum.Sum32()
	e.out = append(e.out, byte(s>>24), byte(s>>16), byte(s>>8), byte(s))
	return nil
}

// emitBlock 输出一个存储块：1 字节块头、2 字节小端 LEN、
// 2 字节小端 NLEN（LEN 的按位取反）、LEN 字节数据。
func (e *Encoder) emitBlock(final bool, data []byte) {
	var hdr byte
	if final {
		hdr = 0x01
	}
	n := uint16(len(data))
	e.out = append(e.out, hdr, byte(n), byte(n>>8), byte(^n), byte(^n>>8))
	e.out = append(e.out, data...)
}

// Bytes 返回目前已输出字节的副本。
func (e *Encoder) Bytes() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), e.out...)
}
