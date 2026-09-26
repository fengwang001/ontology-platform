// Package stream 在 enc 之上提供游标式读取与整段解码。
package stream

import "ontology/enc"

// Reader 是游标式 UTF-8 读取器。被拒绝的 Next 不推进游标。
type Reader struct {
	buf []byte
	pos int
	// lookback 记录最近一次 decodeAll 中为校验过长/代理项/越界
	// 而额外回看或重读的字节数。单趟实现下恒为 0。非导出，不进公开接口。
	lookback int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

// Reset 用新缓冲重置读取器，游标归零。
func (r *Reader) Reset(b []byte) { r.buf, r.pos, r.lookback = b, 0, 0 }

func (r *Reader) Pos() int { return r.pos }
func (r *Reader) Len() int { return len(r.buf) }

// Next 解出游标处的一个 rune 并推进；失败时游标不动。
func (r *Reader) Next() (rune, error) {
	rn, n, err := enc.DecodeRune(r.buf[r.pos:])
	if err != nil {
		return 0, err
	}
	r.pos += n
	return rn, nil
}

// decodeAll 单趟解码整个缓冲，首个错误即整体失败。
func (r *Reader) decodeAll() ([]rune, error) {
	var out []rune
	for r.pos < len(r.buf) {
		rn, err := r.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, rn)
	}
	return out, nil
}

// DecodeAll 整段解码 b；任一字节非法则返回 (nil, error)。
func DecodeAll(b []byte) ([]rune, error) { return NewReader(b).decodeAll() }
