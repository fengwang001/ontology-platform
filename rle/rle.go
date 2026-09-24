// Package rle 实现带转义的文本游程编码：严格 Decode、Encode 与流式解码。
// 格式与拒绝清单的推导见 DESIGN.md。
package rle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"ontology/runs"
)

// 严格解码的拒绝原因，均可用 errors.Is 判定。
var (
	ErrCountOne          = errors.New("rle: 显式次数 1")
	ErrCountZero         = errors.New("rle: 次数 0")
	ErrLeadingZero       = errors.New("rle: 次数带前导零")
	ErrAdjacentSame      = errors.New("rle: 相邻游程符号相同")
	ErrBadEscape         = errors.New("rle: 反斜杠后不是数字或反斜杠")
	ErrTrailingBackslash = errors.New("rle: 末尾孤立反斜杠")
	ErrMissingSymbol     = errors.New("rle: 次数后缺少符号")
	ErrInvalidUTF8       = errors.New("rle: 非法 UTF-8")
	ErrOutputLimit       = errors.New("rle: 输出超过字节上限")
)

// Error 描述一次解码失败：Kind 为哨兵错误，Offset 为违规 token 的字节偏移。
type Error struct {
	Kind   error
	Offset int64
}

func (e *Error) Error() string { return fmt.Sprintf("rle: %v（字节偏移 %d）", e.Kind, e.Offset) }
func (e *Error) Unwrap() error { return e.Kind }

// DefaultMaxOutput 是 NewDecoder 与 Decode 的默认输出总字节上限。
const DefaultMaxOutput = 1 << 30

var examined int64

// ExaminedBytes 返回本包至今检查过的输入字节总数（单趟，每字节恰好一次）。
func ExaminedBytes() int64 { return examined }

// Encode 把 s 编码为规范形式：最长游程，次数 1 省略，数字与反斜杠转义。
func Encode(s string) string {
	var b bytes.Buffer
	b.Grow(len(s))
	for _, r := range runs.Split(s) {
		if r.N > 1 {
			b.Write(runs.AppendCount(nil, r.N))
		}
		if r.Sym == '\\' || '0' <= r.Sym && r.Sym <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(r.Sym)
	}
	return b.String()
}

// Decode 严格解码 t；接受 t 当且仅当 Encode(Decode(t)) == t。
func Decode(t string) (string, error) {
	var b bytes.Buffer
	d := NewDecoder(&b)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Decoder 是流式严格解码器：按任意切分喂入，结果与错误逐位一致。
type Decoder struct {
	w       io.Writer
	max     int64
	written int64
	off     int64 // 下一个待处理字节的偏移
	tok     int64 // 当前游程 token 的起始偏移
	escOff  int64
	digits  []byte
	pend    []byte // 未完整的多字节 UTF-8 序列
	prev    rune
	hasPrev bool
	esc     bool
	err     error
}

// NewDecoder 返回向 w 写出解码结果的解码器，上限为 DefaultMaxOutput。
func NewDecoder(w io.Writer) *Decoder { return &Decoder{w: w, max: DefaultMaxOutput} }

// SetMaxOutput 设置输出总字节上限，须在首次 Write 前调用。
func (d *Decoder) SetMaxOutput(n int64) { d.max = n }

// Write 喂入一段编码文本；每个输入字节恰好被检查一次。
func (d *Decoder) Write(p []byte) (int, error) {
	for i, b := range p {
		if d.err != nil {
			return i, d.err
		}
		examined++
		d.step(b)
		d.off++
	}
	return len(p), d.err
}

// Close 结束输入，检出末尾孤立反斜杠、缺符号的次数与截断的 UTF-8。
func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
	case len(d.pend) > 0:
		d.fail(ErrInvalidUTF8, d.off-int64(len(d.pend)))
	case d.esc:
		d.fail(ErrTrailingBackslash, d.escOff)
	case len(d.digits) > 0:
		d.fail(ErrMissingSymbol, d.tok)
	}
	return d.err
}

func (d *Decoder) fail(kind error, off int64) {
	if d.err == nil {
		d.err = &Error{Kind: kind, Offset: off}
	}
}

func (d *Decoder) step(b byte) {
	if len(d.pend) > 0 || b >= utf8.RuneSelf {
		d.pend = append(d.pend, b)
		r, size := utf8.DecodeRune(d.pend)
		if r == utf8.RuneError && size <= 1 {
			if size == 0 {
				return // 多字节序列尚未完整
			}
			d.fail(ErrInvalidUTF8, d.off-int64(len(d.pend))+1)
			return
		}
		d.pend = d.pend[:0]
		if d.esc {
			d.fail(ErrBadEscape, d.escOff)
			return
		}
		if len(d.digits) == 0 {
			d.tok = d.off - int64(size) + 1
		}
		d.symbol(r)
		return
	}
	switch {
	case d.esc:
		if b == '\\' || '0' <= b && b <= '9' {
			d.esc = false
			d.symbol(rune(b))
		} else {
			d.fail(ErrBadEscape, d.escOff)
		}
	case b == '\\':
		d.esc, d.escOff = true, d.off
		if len(d.digits) == 0 {
			d.tok = d.off
		}
	case '0' <= b && b <= '9':
		if len(d.digits) == 0 {
			d.tok = d.off
		}
		d.digits = append(d.digits, b)
	default:
		if len(d.digits) == 0 {
			d.tok = d.off
		}
		d.symbol(rune(b))
	}
}

func (d *Decoder) symbol(r rune) {
	rem := uint64(d.max - d.written)
	n := uint64(1)
	if len(d.digits) > 0 {
		s := string(d.digits)
		d.digits = d.digits[:0]
		switch {
		case s == "0":
			d.fail(ErrCountZero, d.tok)
			return
		case s == "1":
			d.fail(ErrCountOne, d.tok)
			return
		case s[0] == '0':
			d.fail(ErrLeadingZero, d.tok)
			return
		}
		n = runs.ParseCount(s, rem+1)
	}
	if d.hasPrev && d.prev == r {
		d.fail(ErrAdjacentSame, d.tok)
		return
	}
	if n > rem {
		d.fail(ErrOutputLimit, d.tok)
		return
	}
	d.prev, d.hasPrev = r, true
	d.emit(r, n)
}

func (d *Decoder) emit(r rune, n uint64) {
	var b [utf8.UTFMax]byte
	sz := utf8.EncodeRune(b[:], r)
	chunk := bytes.Repeat(b[:sz], 8192/sz+1)
	for n > 0 && d.err == nil {
		m := min(n, uint64(len(chunk)/sz))
		if _, err := d.w.Write(chunk[:int(m)*sz]); err != nil {
			d.fail(err, d.off)
		}
		d.written += m * uint64(sz)
		n -= m
	}
}
