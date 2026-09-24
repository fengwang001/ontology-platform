// Package stream 提供严格模式的流式 Base64 编解码器；解码器逐字节检查输入，不回扫。
package stream

import (
	"cmp"
	"errors"
	"ontology/b64"
)

var ErrLength = errors.New("stream: input length not a multiple of 4")
var ErrNewline = errors.New("stream: misplaced newline")
var ErrTooLong = errors.New("stream: output limit exceeded")
var ErrClosed = errors.New("stream: coder already closed")

// Error 携带错误类别（Kind，可用 errors.Is 判定）与整个输入流中的字节偏移 Off。
type Error struct {
	Kind error
	Off  int64
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type Decoder struct {
	mime, padded, cr, done bool
	maxOut, nbuf           int
	buf                    [4]int
	out                    []byte
	off                    int64
	err                    error
}

func NewDecoder(mime bool, maxOut int) *Decoder { return &Decoder{mime: mime, maxOut: maxOut} }
func (d *Decoder) Checked() int64               { return d.off } // 已检查字节总数

func (d *Decoder) fail(kind error, off int64) error {
	d.err, d.done = &Error{Kind: kind, Off: off}, true
	return d.err
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.done {
		return 0, cmp.Or(d.err, ErrClosed)
	}
	for i, c := range p {
		if err := d.step(c); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

func (d *Decoder) step(c byte) error {
	off := d.off
	d.off++
	if d.cr && c != '\n' {
		return d.fail(ErrNewline, off-1)
	}
	if c == '\r' || c == '\n' {
		if !d.mime || d.nbuf > 0 {
			return d.fail(ErrNewline, off)
		}
		d.cr = c == '\r'
		return nil
	}
	if d.padded {
		return d.fail(b64.ErrBadPadding, off)
	}
	v, ok := b64.Classify(c)
	if !ok {
		return d.fail(b64.ErrInvalidChar, off)
	}
	if d.buf[d.nbuf], d.nbuf = v, d.nbuf+1; d.nbuf == 4 {
		return d.flush(off - 3)
	}
	return nil
}

func (d *Decoder) flush(groupOff int64) error {
	var tmp [3]byte
	n, bad, err := b64.DecodeVals(d.buf, tmp[:])
	if err != nil {
		return d.fail(err, groupOff+int64(bad))
	}
	if d.maxOut >= 0 && len(d.out)+n > d.maxOut {
		return d.fail(ErrTooLong, groupOff)
	}
	d.out = append(d.out, tmp[:n]...)
	d.nbuf, d.padded = 0, d.buf[2] < 0 || d.buf[3] < 0
	return nil
}

func (d *Decoder) Close() error {
	if d.done {
		return d.err
	}
	if d.cr {
		return d.fail(ErrNewline, d.off-1)
	}
	if d.nbuf != 0 {
		return d.fail(ErrLength, d.off)
	}
	d.done = true
	return nil
}

func (d *Decoder) Output() []byte { return append([]byte{}, d.out...) }

// Encoder 是流式编码器；mime 时每 76 字符插入 \r\n，最后一行后不加。
type Encoder struct {
	mime, closed bool
	buf          [3]byte
	nbuf, col    int
	out          []byte
}

func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	for _, b := range p {
		if e.buf[e.nbuf], e.nbuf = b, e.nbuf+1; e.nbuf == 3 {
			e.emit(e.buf[:3])
			e.nbuf = 0
		}
	}
	return len(p), nil
}

func (e *Encoder) emit(src []byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	g := b64.EncodeGroup(src)
	e.out = append(e.out, g[:]...)
	e.col += 4
}

func (e *Encoder) Close() error {
	if !e.closed && e.nbuf > 0 {
		e.emit(e.buf[:e.nbuf])
	}
	e.closed = true
	return nil
}

func (e *Encoder) Output() []byte { return append([]byte{}, e.out...) }
