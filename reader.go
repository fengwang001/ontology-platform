package ontology

import (
	"encoding/binary"
	"sync"
)

// Reader 是与 Writer 配对的流式校验读取器。Feed 绝不输出半块：
// 只返回本次调用内完整且校验通过的块所解出的数据；本次调用中
// 出错时，返回出错块之前已解出的数据与该错误。出错后读取端粘滞。
//
// 可跳过块（0x80..0xFE）按流式丢弃，不缓冲其负载；读取端缓冲
// 不超过 65544 字节（非导出计数器 maxBuffered 记录峰值）。
type Reader struct {
	mu sync.Mutex

	err         error
	seenID      bool
	pos         int64 // 已消费字节在整个流中的偏移（下一字节）
	chunkStart  int64 // 当前块头第一字节的偏移
	hdr         [4]byte
	hdrN        int
	state       int // 0=收块头, 1=标识块, 2=数据块, 3=可跳过块
	chunkType   byte
	chunkLen    int
	payload     []byte
	plN         int
	maxBuffered int
}

const (
	rStateHeader = iota
	rStateIdentifier
	rStateData
	rStateSkip
)

// NewReader 创建读取器。
func NewReader() *Reader { return &Reader{} }

func (r *Reader) fail(kind error) {
	if r.err == nil {
		r.err = &FrameError{Offset: r.chunkStart, Err: kind}
	}
}

func (r *Reader) noteBuffer(n int) {
	if n > r.maxBuffered {
		r.maxBuffered = n
	}
}

// Feed 处理本次输入，返回完整且校验通过的块所解出的数据。
// 出错时返回出错块之前已解出的数据与 *FrameError；之后调用粘滞，
// 返回同一错误且无输出。
func (r *Reader) Feed(p []byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err != nil {
		return nil, r.err
	}
	var out []byte
	for i := 0; i < len(p); i++ {
		b := p[i]

		if r.state == rStateHeader {
			if r.hdrN == 0 {
				r.chunkStart = r.pos
			}
			r.hdr[r.hdrN] = b
			r.hdrN++
			r.pos++
			r.noteBuffer(r.hdrN)
			if r.hdrN < 4 {
				continue
			}

			typ := r.hdr[0]
			n := int(r.hdr[1]) | int(r.hdr[2])<<8 | int(r.hdr[3])<<16
			r.chunkType = typ
			r.chunkLen = n
			r.hdrN = 0

			if !r.seenID {
				if typ != identifierType {
					r.fail(ErrNoIdentifier)
					break
				}
			}
			switch {
			case typ == identifierType:
				if n != 6 {
					r.fail(ErrBadIdentifier)
					break
				}
				r.payload = make([]byte, 6)
				r.plN = 0
				r.state = rStateIdentifier
				r.noteBuffer(4 + 6)
			case typ == chunkRLE || typ == chunkRaw:
				if n < 4 || n > maxChunkLoad {
					r.fail(ErrChunkLen)
					break
				}
				r.payload = make([]byte, n)
				r.plN = 0
				r.state = rStateData
				r.noteBuffer(4 + n)
			case typ >= 0x02 && typ <= 0x7F:
				r.fail(ErrReserved)
			case typ >= 0x80 && typ <= 0xFE:
				r.state = rStateSkip
			default: // 0x02..0x7F 已在上面覆盖，default 仅为防御
				r.fail(ErrReserved)
			}
			if r.err != nil {
				break
			}
			continue
		}

		r.pos++
		switch r.state {
		case rStateIdentifier:
			r.payload[r.plN] = b
			r.plN++
			if r.plN == 6 {
				if string(r.payload) != "sNaPpY" {
					r.fail(ErrBadIdentifier)
					break
				}
				r.seenID = true
				r.resetToHeader()
			}
		case rStateSkip:
			r.plN++
			if r.plN == r.chunkLen {
				r.resetToHeader()
			}
		case rStateData:
			r.payload[r.plN] = b
			r.plN++
			if r.plN == r.chunkLen {
				data := r.finishData()
				if r.err != nil {
					break
				}
				out = append(out, data...)
			}
		}
		if r.err != nil {
			break
		}
	}

	if r.err != nil {
		if out == nil {
			out = []byte{}
		}
		return out, r.err
	}
	return out, nil
}

func (r *Reader) resetToHeader() {
	r.state = rStateHeader
	r.payload = nil
	r.plN = 0
	r.chunkLen = 0
}

// finishData 在数据块负载收齐时解码并校验。
func (r *Reader) finishData() []byte {
	defer r.resetToHeader()

	sum := binary.LittleEndian.Uint32(r.payload[:4])
	body := r.payload[4:]
	var data []byte
	if r.chunkType == chunkRLE {
		decoded, err := rleDecode(body, maxChunkData)
		if err != nil {
			r.fail(err)
			return nil
		}
		data = decoded
	} else {
		if len(body) > maxChunkData {
			r.fail(ErrDecode)
			return nil
		}
		data = body
	}
	if maskedChecksum(data) != sum {
		r.fail(ErrChecksum)
		return nil
	}
	return append([]byte(nil), data...)
}

// Close 判定流是否完整：出错粘滞返回原错误；空流报 ErrNoIdentifier
// （偏移 0）；停在块头或负载中间报 ErrTruncated。
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.state != rStateHeader || r.hdrN != 0 {
		return &FrameError{Offset: r.chunkStart, Err: ErrTruncated}
	}
	if !r.seenID {
		return &FrameError{Offset: 0, Err: ErrNoIdentifier}
	}
	return nil
}
