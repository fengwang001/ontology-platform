// Package codec 按显式字节序与宽度编解码整数序列。依赖 ord。
package codec

import (
	"errors"

	"ontology/ord"
)

// 可判定的哨兵错误，二者互不相同。
var (
	ErrInvalidWidth = errors.New("codec: width must be 2, 4 or 8")
	ErrMisaligned   = errors.New("codec: buffer length not a multiple of width")
)

func validWidth(w int) bool { return w == 2 || w == 4 || w == 8 }

func put(o ord.ByteOrder, w int, b []byte, v int64) {
	switch w {
	case 2:
		o.PutInt16(b, int16(v))
	case 4:
		o.PutInt32(b, int32(v))
	case 8:
		o.PutInt64(b, v)
	}
}

// get 读回并做符号扩展（有符号语义）。
func get(o ord.ByteOrder, w int, b []byte) int64 {
	switch w {
	case 2:
		return int64(o.Int16(b))
	case 4:
		return int64(o.Int32(b))
	default:
		return o.Int64(b)
	}
}

// Encode 把 vals 按 order/width 写入新缓冲；width 非法返回 nil。
func Encode(order ord.ByteOrder, width int, vals []int64) []byte {
	if !validWidth(width) {
		return nil
	}
	out := make([]byte, len(vals)*width)
	for i, v := range vals {
		put(order, width, out[i*width:], v)
	}
	return out
}

// Decode 整体解码；width 非法或长度不对齐时整体失败，返回 (nil, error)。
func Decode(order ord.ByteOrder, width int, buf []byte) ([]int64, error) {
	if !validWidth(width) {
		return nil, ErrInvalidWidth
	}
	if len(buf)%width != 0 {
		return nil, ErrMisaligned
	}
	out := make([]int64, len(buf)/width)
	for i := range out {
		out[i] = get(order, width, buf[i*width:])
	}
	return out, nil
}

// Decoder 是游标式解码器：定宽 + 直接偏移，每个整数 O(1)。
type Decoder struct {
	order    ord.ByteOrder
	width    int
	buf      []byte
	pos      int
	examined int // 已检查字节数：每字节恰好读一次
	lookback int // 为定位第 k 个整数而回看/重扫的字节数，恒为 0
}

func NewDecoder(order ord.ByteOrder, width int, buf []byte) (*Decoder, error) {
	if !validWidth(width) {
		return nil, ErrInvalidWidth
	}
	if len(buf)%width != 0 {
		return nil, ErrMisaligned
	}
	return &Decoder{order: order, width: width, buf: buf}, nil
}

// Next 解出下一个整数；ok=false 表示已读完。
func (d *Decoder) Next() (v int64, ok bool) {
	if d.pos >= len(d.buf) {
		return 0, false
	}
	v = get(d.order, d.width, d.buf[d.pos:])
	d.examined += d.width
	d.pos += d.width
	return v, true
}
