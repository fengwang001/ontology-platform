// Package frame 把若干变长字节串字段按长度前缀拼成一条记录，并能切分回来。
package frame

import (
	"errors"

	"ontology/lenp"
)

// ErrTruncated 长度前缀声明的负载超过剩余缓冲，即记录被截断。
var ErrTruncated = errors.New("frame: length prefix exceeds remaining bytes")

// Encode 把字段顺序拼接：每字段 = 4 字节小端长度前缀 + 负载。长度 0 的空字段合法。
func Encode(fields [][]byte) []byte {
	total := 0
	for _, f := range fields {
		total += lenp.Len + len(f)
	}
	buf := make([]byte, 0, total)
	for _, f := range fields {
		buf = append(buf, lenp.PutLength(len(f))...)
		buf = append(buf, f...)
	}
	return buf
}

// Decode 从偏移 0 起逐字段切分整个缓冲。任何截断/非法前缀都整体失败，返回 (nil, error)。
func Decode(buf []byte) ([][]byte, error) {
	r := NewReader(buf)
	var out [][]byte
	for r.Pos() < len(buf) {
		f, err := r.NextField()
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// Reader 是游标式解码器，只靠长度前缀做偏移算术，不扫描负载。
type Reader struct {
	buf []byte
	pos int
	// lastSkipPayload 记录最近一次 SkipField 触碰（读）的负载字节数，恒为 0。
	lastSkipPayload int
}

// NewReader 返回从 buf 偏移 0 开始的游标。
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Pos 返回当前偏移。
func (r *Reader) Pos() int { return r.pos }

// NextField 读出当前字段负载并推进游标；失败不推进。
func (r *Reader) NextField() ([]byte, error) {
	l, err := r.peek()
	if err != nil {
		return nil, err
	}
	f := r.buf[r.pos+lenp.Len : r.pos+lenp.Len+l]
	r.pos += lenp.Len + l
	return f, nil
}

// SkipField 只读 4 字节前缀、把偏移加 4+L，不触碰负载；失败不推进游标。
func (r *Reader) SkipField() error {
	l, err := r.peek()
	if err != nil {
		return err
	}
	r.pos += lenp.Len + l
	r.lastSkipPayload = 0
	return nil
}

// peek 读当前位置的长度前缀并校验负载不越界，游标不动。
func (r *Reader) peek() (int, error) {
	l, err := lenp.GetLength(r.buf[r.pos:])
	if err != nil {
		return 0, err
	}
	if l > len(r.buf)-r.pos-lenp.Len {
		return 0, ErrTruncated
	}
	return l, nil
}
