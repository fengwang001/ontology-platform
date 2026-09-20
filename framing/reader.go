// Package framing 实现一个流式分帧读取器：把任意切分的字节块增量地
// 切成长度前缀帧（4 字节大端长度 N + N 字节负载）。
package framing

import "encoding/binary"

const headerLen = 4

// shrinkThreshold 是缓冲区在清空后允许保留的最大容量，
// 超过则丢弃底层数组，避免内存随流长度线性增长。
const shrinkThreshold = 4096

// Reader 增量地把字节流切分为一条条完整帧。
// 零值不可用，请使用 New 构造。
type Reader struct {
	max      int
	buf      []byte
	err      error // 终止态错误（如 ErrFrameTooLarge）
	closed   bool
	closeErr error
}

// New 返回一个 Reader，maxFrame 为单帧负载长度上限（字节）。
func New(maxFrame int) *Reader {
	if maxFrame < 0 {
		maxFrame = 0
	}
	return &Reader{max: maxFrame}
}

// Buffered 返回当前尚未成帧的残留字节数。
func (r *Reader) Buffered() int { return len(r.buf) }

// Feed 喂入任意长度（含 0 长度）的一块字节，返回本次新切出的完整帧。
// 返回的帧均已拷贝，与 p 及内部缓冲区完全隔离。
func (r *Reader) Feed(p []byte) ([][]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.closed {
		return nil, ErrClosed
	}
	r.buf = append(r.buf, p...)

	var frames [][]byte
	consumed := 0
	for {
		rest := r.buf[consumed:]
		if len(rest) < headerLen {
			break
		}
		n := int(binary.BigEndian.Uint32(rest))
		if n > r.max {
			// 进入终止态：不再消费任何字节，Buffered 保持不变。
			r.err = ErrFrameTooLarge
			return frames, r.err
		}
		if len(rest) < headerLen+n {
			break
		}
		frame := make([]byte, n)
		copy(frame, rest[headerLen:])
		frames = append(frames, frame)
		consumed += headerLen + n
	}
	if consumed > 0 {
		r.compact(consumed)
	}
	return frames, nil
}

// compact 移除已消费的字节，并在缓冲区清空且容量过大时释放底层数组。
func (r *Reader) compact(consumed int) {
	rest := len(r.buf) - consumed
	copy(r.buf, r.buf[consumed:])
	r.buf = r.buf[:rest]
	if rest == 0 && cap(r.buf) > shrinkThreshold {
		r.buf = nil
	}
}

// Close 表示流结束：若缓冲区还有未成帧的残留字节，返回 ErrIncomplete。
// Close 可重复调用且结果一致；Close 之后再 Feed 一律返回 ErrClosed。
func (r *Reader) Close() error {
	if r.closed {
		return r.closeErr
	}
	r.closed = true
	switch {
	case r.err != nil:
		r.closeErr = r.err
	case len(r.buf) > 0:
		r.closeErr = ErrIncomplete
	}
	return r.closeErr
}
