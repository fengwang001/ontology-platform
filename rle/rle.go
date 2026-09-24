// Package rle 在 runs 之上提供带转义的文本游程编码：编码、严格解码与流式解码。
package rle

import (
	"errors"
	"io"
	"math/big"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne            = errors.New("rle: explicit count of 1")
	ErrCountZero           = errors.New("rle: run count of 0")
	ErrLeadingZero         = errors.New("rle: leading zero in count")
	ErrAdjacentSame        = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape           = errors.New("rle: bad escape")
	ErrTrailingBackslash   = errors.New("rle: trailing backslash")
	ErrTrailingCount       = errors.New("rle: count without symbol")
	ErrInvalidUTF8         = errors.New("rle: invalid UTF-8")
	ErrOutputLimitExceeded = errors.New("rle: decoded output exceeds limit")
)

// Error 携带错误类别与输入字节偏移（从 0 开始）。
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

const DefaultDecodeLimit = 64 << 20

// Encode 返回 s 的唯一规范 RLE 文本。
func Encode(s string) string {
	var b strings.Builder
	for _, r := range runs.Split([]rune(s)) {
		b.WriteString(runs.FormatCount(r.Count))
		if runs.NeedEscape(r.Sym) {
			b.WriteByte('\\')
		}
		b.WriteRune(r.Sym)
	}
	return b.String()
}

// Decode 严格解码；任何使 Encode(Decode(t)) != t 的输入都被拒绝。
func Decode(t string) (string, error) {
	var b strings.Builder
	d := NewWriter(&b, DefaultDecodeLimit)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Writer 是增量严格解码器；跨任意切分点的结果与错误一致。
type Writer struct {
	w              io.Writer
	Limit          int // 输出字节上限，<=0 不限
	examined       int
	out            int64
	sticky         error
	buf            []byte // 未形成完整 token 的字节
	off            int    // buf[0] 的绝对偏移
	afterCount     bool   // 已收齐次数、等待符号
	digits         []byte
	digitStart     int
	prevSym        rune
	havePrev       bool
	sawSlash       bool // buf 当前以孤立的反斜杠开头
}

func NewWriter(w io.Writer, limit int) *Writer { return &Writer{w: w, Limit: limit} }

// Write 喂入任意字节片段；每字节在 Examined 中恰好计一次。
func (d *Writer) Write(p []byte) (int, error) {
	if d.sticky == nil {
		d.examined += len(p)
		d.buf = append(d.buf, p...)
		if err := d.process(); err != nil {
			d.sticky = err
		}
	}
	return len(p), d.sticky
}

// Close 校验末尾不得残留次数、反斜杠或残缺 UTF-8。
func (d *Writer) Close() error {
	switch {
	case d.sticky != nil:
	case d.sawSlash:
		d.sticky = d.fail(d.off, ErrTrailingBackslash)
	case d.afterCount:
		d.sticky = d.fail(d.digitStart, ErrTrailingCount)
	case len(d.buf) > 0:
		d.sticky = d.fail(d.off, ErrInvalidUTF8)
	}
	return d.sticky
}

func (d *Writer) Examined() int { return d.examined }

func (d *Writer) fail(off int, err error) error {
	return &Error{Offset: off, Err: err}
}

func (d *Writer) keep(n int) { // 丢弃前 n 个已确定字节，保留剩余前缀
	d.buf = append(d.buf[:0], d.buf[n:]...)
	d.off += n
}

func (d *Writer) process() error {
	for {
		if d.sawSlash {
			if len(d.buf) < 2 {
				return nil
			}
			c := d.buf[1]
			if c != '\\' && (c < '0' || c > '9') {
				return d.fail(d.off, ErrBadEscape)
			}
			if err := d.emit(1, rune(c)); err != nil {
				return err
			}
			d.sawSlash = false
			d.keep(2)
			continue
		}
		if len(d.buf) == 0 {
			return nil
		}
		switch c := d.buf[0]; {
		case c >= '0' && c <= '9':
			n := 0
			for n < len(d.buf) && d.buf[n] >= '0' && d.buf[n] <= '9' {
				n++
			}
			if len(d.digits) == 0 {
				d.digitStart = d.off
			}
			d.digits = append(d.digits, d.buf[:n]...)
			d.afterCount = true
			d.keep(n)
		case c == '\\':
			if len(d.buf) < 2 {
				d.sawSlash = true
				return nil
			}
			next := d.buf[1]
			if next != '\\' && (next < '0' || next > '9') {
				return d.fail(d.off, ErrBadEscape)
			}
			if err := d.emit(1, rune(next)); err != nil {
				return err
			}
			d.keep(2)
		default:
			r, size, incomplete, err := d.decodeSymbol()
			if err != nil || incomplete {
				return err
			}
			if err := d.emit(0, r); err != nil {
				return err
			}
			d.keep(size)
		}
	}
}

// decodeSymbol 解析 buf 起始处的符号；incomplete 表示只是残缺 UTF-8 前缀。
func (d *Writer) decodeSymbol() (rune, int, bool, error) {
	b := d.buf[0]
	if b < utf8.RuneSelf || utf8.FullRune(d.buf) {
		r, size := utf8.DecodeRune(d.buf)
		if r == utf8.RuneError && size == 1 {
			return 0, 0, false, d.fail(d.off, ErrInvalidUTF8)
		}
		return r, size, false, nil
	}
	if b == 0xC0 || b == 0xC1 || b > 0xF4 {
		return 0, 0, false, d.fail(d.off, ErrInvalidUTF8)
	}
	return 0, 0, true, nil
}

// emit 校验次数与相邻符号，然后检查上限并写出；rel 是符号在 buf 中的偏移。
func (d *Writer) emit(rel int, sym rune) error {
	n := big.NewInt(1)
	symOff := d.off + rel
	if d.afterCount {
		parsed, kind, err := runs.ParseCount(string(d.digits), 0, len(d.digits))
		if err != nil {
			return d.fail(d.digitStart, err)
		}
		n = parsed
		switch {
		case n.Sign() == 0:
			return d.fail(d.digitStart, ErrCountZero)
		case kind == runs.CountLeading:
			return d.fail(d.digitStart, ErrLeadingZero)
		case n.Cmp(big.NewInt(1)) == 0:
			return d.fail(d.digitStart, ErrCountOne)
		}
	}
	if d.havePrev && d.prevSym == sym {
		return d.fail(symOff, ErrAdjacentSame)
	}
	if d.afterCount {
		width := int64(utf8.RuneLen(sym))
		if d.Limit > 0 {
			avail := big.NewInt((int64(d.Limit) - d.out) / width)
			if n.Cmp(avail) > 0 {
				return d.fail(symOff, ErrOutputLimitExceeded)
			}
		}
		piece, rem := string(sym), new(big.Int).Set(n)
		chunk := big.NewInt(1 << 16)
		for rem.Sign() > 0 {
			k := int64(1 << 16)
			if rem.Cmp(chunk) < 0 {
				k = rem.Int64()
			}
			if _, err := io.WriteString(d.w, strings.Repeat(piece, int(k))); err != nil {
				return err
			}
			rem.Sub(rem, big.NewInt(k))
			d.out += k * width
		}
	} else {
		if _, err := io.WriteString(d.w, string(sym)); err != nil {
			return err
		}
		d.out += int64(utf8.RuneLen(sym))
	}
	d.digits = d.digits[:0]
	d.afterCount = false
	d.prevSym, d.havePrev = sym, true
	return nil
}
