// Package frame 实现以 '\n' 为分隔符、'\' 为转义字节的定界帧编解码。
// 解码为单趟线性：每个字节恰好被检查一次，从不回看。
package frame

import (
	"errors"
	"sync/atomic"

	"ontology/esc"
)

// ErrMissingTerminator 表示流不以 '\n' 结尾（最后一帧没有终止符）。
var ErrMissingTerminator = errors.New("frame: stream does not end with delimiter")

// 非导出计数器：最近一次 Decode 的字节检查数与回看/重读字节数。
// 仅包内测试可直接读取，不出现在任何公开接口。
var (
	lastChecked  atomic.Int64
	lastLookback atomic.Int64
)

// Encode 把帧列表编成字节流：逐帧转义，帧后跟一个 '\n'。
func Encode(frames [][]byte) []byte {
	out := make([]byte, 0)
	for _, f := range frames {
		out = append(out, esc.Escape(f)...)
		out = append(out, esc.Newline)
	}
	return out
}

// scanFrame 从 buf[start] 开始单趟扫描一帧，返回反转义后的负载、
// 下一帧起始下标与本次检查的字节数；出错时不消耗任何输入。
func scanFrame(buf []byte, start int) (payload []byte, next, checked int, err error) {
	payload = make([]byte, 0)
	i := start
	for i < len(buf) {
		b := buf[i]
		checked++
		switch b {
		case esc.Newline:
			return payload, i + 1, checked, nil
		case esc.EscapeByte:
			if i+1 >= len(buf) {
				return nil, 0, checked, esc.ErrDanglingEscape
			}
			checked++
			switch buf[i+1] {
			case esc.EscapeByte:
				payload = append(payload, esc.EscapeByte)
			case 'n':
				payload = append(payload, esc.Newline)
			case esc.Newline:
				return nil, 0, checked, esc.ErrDanglingEscape
			default:
				return nil, 0, checked, esc.ErrInvalidEscape
			}
			i++
		default:
			payload = append(payload, b)
		}
		i++
	}
	return nil, 0, checked, ErrMissingTerminator
}

// Decode 单趟解码整段流。任何错误都使整体失败，返回 (nil, error)。
// 空流合法，解码为零帧。
func Decode(buf []byte) ([][]byte, error) {
	frames := make([][]byte, 0)
	var checked, lookback int64
	pos := 0
	for pos < len(buf) {
		payload, next, c, err := scanFrame(buf, pos)
		checked += int64(c)
		if err != nil {
			lastChecked.Store(checked)
			lastLookback.Store(lookback)
			return nil, err
		}
		frames = append(frames, payload)
		pos = next
	}
	lastChecked.Store(checked)
	lastLookback.Store(lookback)
	return frames, nil
}

// Reader 是游标式逐帧解码器。
type Reader struct {
	buf []byte
	pos int
}

// NewReader 返回从 buf 起始处读取的 Reader。
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Pos 返回当前游标位置（已消费的字节数）。
func (r *Reader) Pos() int { return r.pos }

// NextFrame 返回下一帧的反转义负载。失败时游标不推进，Reader 可继续正常使用。
func (r *Reader) NextFrame() ([]byte, error) {
	payload, next, _, err := scanFrame(r.buf, r.pos)
	if err != nil {
		return nil, err
	}
	r.pos = next
	return payload, nil
}
