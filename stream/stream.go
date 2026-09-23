// Package stream 在 b64 之上提供严格模式的流式 Base64 编解码器。
package stream

import (
	"errors"
	"fmt"

	"ontology/b64"
)

var (
	ErrChar    = errors.New("stream: 非法字符")
	ErrTail    = errors.New("stream: 非规范尾部")
	ErrPadding = errors.New("stream: 填充位置错误")
	ErrLength  = errors.New("stream: 流长度不是 4 的倍数")
	ErrNewline = errors.New("stream: 换行位置错误")
	ErrLimit   = errors.New("stream: 超出输出字节上限")
	ErrClosed  = errors.New("stream: 编码器已关闭")
)

// Error 携带错误类别（Kind 为上面某个哨兵）与全局字节偏移。
type Error struct {
	Kind error
	Off  int
}

func (e *Error) Error() string { return fmt.Sprintf("%v（偏移 %d）", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

// Decoder 是严格模式流式解码器；出错后进入终态，后续调用返回同一错误。
type Decoder struct {
	mime      bool
	limit     int
	out       []byte
	v         [4]byte
	n, pads   int
	ended     bool
	crPending bool
	nlSeen    bool
	err       error
	checked   int
	off       int
}

// NewDecoder 创建解码器；mime 时允许组间 \r\n 或 \n；limit<=0 表示无上限。
func NewDecoder(mime bool, limit int) *Decoder { return &Decoder{mime: mime, limit: limit} }

func (d *Decoder) fail(kind error, off int) error {
	if d.err == nil {
		d.err = &Error{Kind: kind, Off: off}
	}
	return d.err
}

// Write 逐字节处理；每个输入字节恰好令 Checked 计数加 1（不回扫）。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, c := range p {
		off := d.off
		d.off++
		d.checked++
		switch {
		case d.crPending:
			d.crPending = false
			if c != '\n' {
				return i, d.fail(ErrNewline, off)
			}
			d.nlSeen = true
		case c == '\r' || c == '\n':
			if !d.mime || d.n > 0 || d.pads > 0 || d.ended || d.nlSeen {
				return i, d.fail(ErrNewline, off)
			}
			d.nlSeen, d.crPending = true, c == '\r'
		case b64.IsPad(c):
			if d.ended || d.n < 2 || d.pads >= 2 || d.n+d.pads >= 4 {
				return i, d.fail(ErrPadding, off)
			}
			d.pads++
		default:
			if d.pads > 0 || d.ended {
				return i, d.fail(ErrPadding, off)
			}
			v, ok := b64.Value(c)
			if !ok {
				return i, d.fail(ErrChar, off)
			}
			d.v[d.n] = v
			d.n++
			d.nlSeen = false
		}
		if d.n+d.pads != 4 {
			continue
		}
		var tmp [3]byte
		m, err := b64.Assemble(d.v, d.pads, &tmp)
		if err != nil {
			return i, d.fail(ErrTail, off-d.pads)
		}
		if d.limit > 0 && len(d.out)+m > d.limit {
			return i, d.fail(ErrLimit, off+1)
		}
		d.out = append(d.out, tmp[:m]...)
		if d.pads > 0 {
			d.ended = true
		}
		d.n, d.pads, d.nlSeen = 0, 0, false
	}
	return len(p), nil
}

// Close 校验流末尾：不允许悬挂的 \r 与未完成的组。
func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
		return d.err
	case d.crPending:
		return d.fail(ErrNewline, d.off)
	case d.n+d.pads != 0:
		return d.fail(ErrLength, d.off)
	}
	return nil
}

// Output 返回已解码字节的副本；Checked 返回输入字节被检查的总次数。
func (d *Decoder) Output() []byte { return append([]byte(nil), d.out...) }
func (d *Decoder) Checked() int   { return d.checked }

// Encoder 是流式 Base64 编码器；mime 时每 76 字符插入一个 \r\n，末尾不换行。
type Encoder struct {
	mime bool
	buf  [3]byte
	nb   int
	col  int
	out  []byte
	done bool
}

// NewEncoder 创建编码器。
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) emit(s []byte) {
	for len(s) > 0 {
		if e.mime && e.col == 76 {
			e.out, e.col = append(e.out, '\r', '\n'), 0
		}
		n := len(s)
		if e.mime && e.col+n > 76 {
			n = 76 - e.col
		}
		e.out = append(e.out, s[:n]...)
		e.col, s = e.col+n, s[n:]
	}
}

// Write 缓存不足一组的尾部，其余立即编码输出；Close 冲刷并补 '='。
func (e *Encoder) Write(p []byte) (int, error) {
	if e.done {
		return 0, ErrClosed
	}
	for _, c := range p {
		e.buf[e.nb] = c
		e.nb++
		if e.nb == 3 {
			e.emit(b64.Encode(nil, e.buf[:3]))
			e.nb = 0
		}
	}
	return len(p), nil
}

// Close 冲刷尾部 1~2 字节并补 '='。
func (e *Encoder) Close() error {
	if e.done {
		return ErrClosed
	}
	if e.nb > 0 {
		e.emit(b64.Encode(nil, e.buf[:e.nb]))
	}
	e.nb, e.done = 0, true
	return nil
}

// Output 返回已编码文本的副本。
func (e *Encoder) Output() []byte { return append([]byte(nil), e.out...) }
