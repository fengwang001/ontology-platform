// Package codec 把 []int64 按指定字节序与字段宽度编解码为字节流。依赖 ord。
package codec

import (
	"errors"

	"ontology/ord"
)

// 可判定的哨兵错误，二者互不相同。
var (
	ErrWidth = errors.New("codec: width must be 2, 4 or 8")
	ErrAlign = errors.New("codec: buffer length not a multiple of width")
)

func validWidth(w int) bool { return w == 2 || w == 4 || w == 8 }

// Encode 按 order 与 width 把 vals 依次写入新缓冲；width 非法时返回 nil。
func Encode(order ord.ByteOrder, width int, vals []int64) []byte {
	if !validWidth(width) {
		return nil
	}
	buf := make([]byte, len(vals)*width)
	c := cursor{order: order, width: width, buf: buf}
	for _, v := range vals {
		c.put(v)
	}
	return buf
}

// Decode 按 order 与 width 解码整段缓冲。宽度非法或长度不对齐时整体失败，
// 返回 (nil, error)，不返回部分结果。
func Decode(order ord.ByteOrder, width int, buf []byte) ([]int64, error) {
	if !validWidth(width) {
		return nil, ErrWidth
	}
	if len(buf)%width != 0 {
		return nil, ErrAlign
	}
	vals := make([]int64, len(buf)/width)
	c := cursor{order: order, width: width, buf: buf}
	for i := range vals {
		vals[i] = c.next()
	}
	return vals, nil
}

// cursor 以直接偏移逐字段走查缓冲，定位第 k 个整数的成本是 O(1)。
// rescans 与 checked 是非导出的计量字段，只证明定位零回看、每字节恰读一次，
// 不经由任何公开接口暴露。
type cursor struct {
	order   ord.ByteOrder
	width   int
	buf     []byte
	off     int
	rescans int // 为定位当前字段而回看/重扫的字节数；直接偏移，恒为 0
	checked int // 至今已检查的字节数
}

func (c *cursor) next() int64 {
	v := getAt(c.order, c.width, c.buf[c.off:c.off+c.width])
	c.off += c.width
	c.checked += c.width
	return v
}

func (c *cursor) put(v int64) {
	putAt(c.order, c.width, c.buf[c.off:c.off+c.width], v)
	c.off += c.width
	c.checked += c.width
}

func getAt(o ord.ByteOrder, w int, b []byte) int64 {
	switch w {
	case 2:
		return int64(ord.Int16(o, b))
	case 4:
		return int64(ord.Int32(o, b))
	default:
		return ord.Int64(o, b)
	}
}

func putAt(o ord.ByteOrder, w int, b []byte, v int64) {
	switch w {
	case 2:
		ord.PutInt16(o, b, int16(v))
	case 4:
		ord.PutInt32(o, b, int32(v))
	default:
		ord.PutInt64(o, b, v)
	}
}
