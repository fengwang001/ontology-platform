package frame

import "sync"

// Reader 是流式校验读取器。Feed 返回本次调用内完整且校验通过的块所解出
// 的数据，绝不输出半块；可跳过块按流式丢弃，不缓冲其负载；缓冲峰值不超过
// 65544 字节。所有方法可并发调用。
type Reader struct {
	mu sync.Mutex

	buf     []byte // 当前不完整块的头部与负载（仅数据块/标识块）
	hdrDone bool   // 当前块头已校验
	next    int64  // 下一个待消费输入字节在流中的绝对偏移

	skipRemain   int   // 可跳过块待丢弃的负载字节数
	skipChunkOff int64 // 可跳过块头的绝对偏移

	firstChunk bool // 尚未处理过任何块
	err        error
	closed     bool
	closeErr   error

	maxBuffered int // len(buf) 的峰值
}

// NewReader 创建读取器。
func NewReader() *Reader {
	return &Reader{firstChunk: true}
}

// Feed 喂入一段流字节，返回本次调用内完整且校验通过的块所解出的数据。
// 本次调用中出错时，同时返回出错块之前已解出的数据与该错误；出错后读取端
// 粘滞，之后 Feed 返回同一错误且无输出。
func (r *Reader) Feed(p []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	if r.closed {
		return nil, ErrClosed
	}
	var out []byte
	for len(p) > 0 {
		if r.skipRemain > 0 {
			k := min(r.skipRemain, len(p))
			p = p[k:]
			r.skipRemain -= k
			r.next += int64(k)
			continue
		}
		need := headerLen
		if r.hdrDone {
			need = headerLen + payloadLen(r.buf)
		}
		k := min(need-len(r.buf), len(p))
		r.buf = append(r.buf, p[:k]...)
		p = p[k:]
		r.next += int64(k)
		if len(r.buf) > r.maxBuffered {
			r.maxBuffered = len(r.buf)
		}
		if !r.hdrDone && len(r.buf) >= headerLen {
			if err := r.checkHeader(); err != nil {
				return out, r.fail(err)
			}
			if r.skipRemain > 0 || isSkippable(r.buf[0]) {
				// 可跳过块：头部已消费，负载按流式丢弃。
				r.buf = r.buf[:0]
				continue
			}
			r.hdrDone = true
		}
		if r.hdrDone && len(r.buf) == headerLen+payloadLen(r.buf) {
			data, err := r.processChunk()
			if err != nil {
				return out, r.fail(err)
			}
			out = append(out, data...)
			r.buf = r.buf[:0]
			r.hdrDone = false
		}
	}
	return out, nil
}

// Close 判定流是否完整：流为空报 ErrNoIdentifier（偏移 0）；停在块头或
// 负载中间报 ErrTruncated。关闭后 Feed 返回关闭时的错误（若有）。
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.closed {
		return r.closeErr
	}
	r.closed = true
	switch {
	case r.firstChunk && len(r.buf) == 0:
		r.closeErr = &FrameError{Offset: 0, Err: ErrNoIdentifier}
	case r.skipRemain > 0:
		r.closeErr = &FrameError{Offset: r.skipChunkOff, Err: ErrTruncated}
	case len(r.buf) > 0:
		r.closeErr = &FrameError{Offset: r.next - int64(len(r.buf)), Err: ErrTruncated}
	}
	r.err = r.closeErr
	return r.closeErr
}

// fail 记录粘滞错误并返回它。调用方须持有锁。
func (r *Reader) fail(err error) error {
	r.err = err
	return err
}

// chunkOff 返回当前块头第一个字节的绝对偏移。调用方须持有锁且 len(buf) > 0。
func (r *Reader) chunkOff() int64 {
	return r.next - int64(len(r.buf))
}

// payloadLen 解析 buf 中块头的负载长度。
func payloadLen(hdr []byte) int {
	return int(hdr[1]) | int(hdr[2])<<8 | int(hdr[3])<<16
}

// checkHeader 在块头收齐时判定头部错误。调用方须持有锁。
func (r *Reader) checkHeader() error {
	typ := r.buf[0]
	n := payloadLen(r.buf)
	if r.firstChunk {
		if typ != chunkIdentifier {
			return &FrameError{Offset: r.chunkOff(), Err: ErrNoIdentifier}
		}
		r.firstChunk = false
	}
	switch {
	case typ == chunkIdentifier:
		if n != len(identifierPayload) {
			return &FrameError{Offset: r.chunkOff(), Err: ErrBadIdentifier}
		}
	case typ == chunkCompressed || typ == chunkRaw:
		if n < 4 || n > maxPayload {
			return &FrameError{Offset: r.chunkOff(), Err: ErrChunkLen}
		}
	case isSkippable(typ):
		r.skipRemain = n
		r.skipChunkOff = r.chunkOff()
	default: // 0x02 到 0x7F：保留的不可跳过块
		return &FrameError{Offset: r.chunkOff(), Err: ErrReserved}
	}
	return nil
}

// processChunk 处理一个收齐的标识块或数据块，返回解出的数据。
// 调用方须持有锁。
func (r *Reader) processChunk() ([]byte, error) {
	typ := r.buf[0]
	payload := r.buf[headerLen:]
	if typ == chunkIdentifier {
		if string(payload) != identifierPayload {
			return nil, &FrameError{Offset: r.chunkOff(), Err: ErrBadIdentifier}
		}
		return nil, nil
	}
	sum := uint32(payload[0]) | uint32(payload[1])<<8 |
		uint32(payload[2])<<16 | uint32(payload[3])<<24
	data := payload[4:]
	var dec []byte
	if typ == chunkCompressed {
		var err error
		dec, err = decompress(data, maxUncompressed)
		if err != nil {
			return nil, &FrameError{Offset: r.chunkOff(), Err: ErrDecode}
		}
	} else {
		dec = data
	}
	if maskedChecksum(dec) != sum {
		return nil, &FrameError{Offset: r.chunkOff(), Err: ErrChecksum}
	}
	return dec, nil
}
