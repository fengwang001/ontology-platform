// Package stream 提供严格模式的流式 Base64 解码器与编码器。
package stream

import (
	"errors"
	"fmt"

	"ontology/b64"
)

// 流级错误哨兵；组级错误（非法字符/填充/非规范尾部）见 b64 包。
var (
	ErrLength  = errors.New("stream: length not a multiple of 4 at end of stream")
	ErrNewline = errors.New("stream: misplaced newline")
	ErrLimit   = errors.New("stream: output limit exceeded")
)

// Error 把错误类别与整个输入流中的字节偏移绑定。
type Error struct {
	Kind error
	Off  int
}

func (e *Error) Error() string { return fmt.Sprintf("%v (offset %d)", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

// Decoder 是严格模式的流式解码器。
type Decoder struct {
	mime    bool
	max     int
	out     []byte
	group   [4]byte
	gn      int
	off     int
	checked int
	cr      bool
	fin     bool
	err     error
}

// NewDecoder 返回解码器；mime 为真时允许组间 \r\n 与 \n，
// max > 0 时限制输出字节数（超限在组边界停下并进入终态）。
func NewDecoder(mime bool, max int) *Decoder { return &Decoder{mime: mime, max: max} }

// Checked 返回被检查过的输入字节总数（每字节恰好一次）。
func (d *Decoder) Checked() int { return d.checked }

// Output 返回已解码的输出。
func (d *Decoder) Output() []byte { return d.out }

func (d *Decoder) fail(kind error, off int) error {
	if d.err == nil {
		d.err = &Error{Kind: kind, Off: off}
	}
	return d.err
}

// Write 逐字节消费输入；出错后解码器进入终态。
func (d *Decoder) Write(p []byte) (int, error) {
	for i, c := range p {
		if d.err != nil {
			return i, d.err
		}
		d.checked++
		if err := d.step(c); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

func (d *Decoder) step(c byte) error {
	o := d.off
	d.off++
	if d.cr {
		d.cr = false
		if c != '\n' {
			return d.fail(ErrNewline, o) // 单独的 \r
		}
		return nil
	}
	switch {
	case c == '\r':
		if !d.mime || d.gn != 0 {
			return d.fail(ErrNewline, o)
		}
		d.cr = true
	case c == '\n':
		if !d.mime || d.gn != 0 {
			return d.fail(ErrNewline, o)
		}
	case c == '=' || b64.Value(c) >= 0:
		if d.fin {
			return d.fail(b64.ErrPadding, o) // 填充只能出现在流末尾
		}
		d.group[d.gn] = c
		d.gn++
		if d.gn == 4 {
			return d.flush(o - 3)
		}
	default:
		return d.fail(b64.ErrChar, o)
	}
	return nil
}

func (d *Decoder) flush(start int) error {
	var dst [3]byte
	n, pos, err := b64.DecodeGroup(d.group, dst[:])
	if err != nil {
		return d.fail(err, start+pos)
	}
	d.gn = 0
	if n < 3 {
		d.fin = true
	}
	if d.max > 0 && len(d.out)+n > d.max {
		return d.fail(ErrLimit, start)
	}
	d.out = append(d.out, dst[:n]...)
	return nil
}

// Close 结束输入并做流级收尾校验。
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.cr {
		return d.fail(ErrNewline, d.off-1)
	}
	if d.gn != 0 {
		return d.fail(ErrLength, d.off)
	}
	return nil
}

// Encoder 是流式编码器；mime 为真时每 76 个字符插入一个 \r\n
// （最后一行之后不加）。
type Encoder struct {
	mime bool
	out  []byte
	buf  [3]byte
	bn   int
	col  int
	done bool
}

// NewEncoder 返回编码器。
func NewEncoder(mime bool) *Encoder { return &Encoder{mime: mime} }

// Output 返回已编码的输出。
func (e *Encoder) Output() []byte { return e.out }

// Write 编码 p，内部缓存不足 3 字节的余数。
func (e *Encoder) Write(p []byte) (int, error) {
	if e.done {
		return 0, errors.New("stream: write on closed encoder")
	}
	for _, c := range p {
		e.buf[e.bn] = c
		e.bn++
		if e.bn == 3 {
			e.emit(e.buf[:3])
			e.bn = 0
		}
	}
	return len(p), nil
}

func (e *Encoder) emit(src []byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	var g [4]byte
	b64.EncodeGroup(src, g[:])
	e.out = append(e.out, g[:]...)
	e.col += 4
}

// Close 冲刷尾部余数字节（按需补 '='）。
func (e *Encoder) Close() error {
	if e.done {
		return nil
	}
	e.done = true
	if e.bn > 0 {
		e.emit(e.buf[:e.bn])
		e.bn = 0
	}
	return nil
}
