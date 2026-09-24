// Package rle 实现带转义的文本游程编码编解码。
// 游程 = 可选十进制次数 + 一个符号；次数 1 省略；数字与反斜杠转义。
package rle

import (
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne          = errors.New("rle: explicit count of 1")
	ErrCountZero         = errors.New("rle: count of 0")
	ErrCountLeadingZero  = errors.New("rle: count has leading zero")
	ErrAdjacentSame      = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape         = errors.New("rle: escape must precede a digit or backslash")
	ErrTrailingBackslash = errors.New("rle: trailing backslash")
	ErrTrailingCount     = errors.New("rle: trailing count without symbol")
	ErrInvalidUTF8       = errors.New("rle: invalid UTF-8 in input")
	ErrCountTooLarge     = errors.New("rle: count exceeds MaxCount")
	ErrOutputLimit       = errors.New("rle: output limit exceeded")
)

// DecodeError 携带哨兵原因与字节偏移；用 errors.Is 判定类别。
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%s at byte offset %d", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

// Encode 返回 s 的规范编码（最长游程、无 Unicode 规范化）。
func Encode(s string) string {
	buf := make([]byte, 0, len(s))
	for _, rn := range runs.SplitRunes([]rune(s)) {
		if rn.N >= 2 {
			buf = runs.AppendCount(buf, rn.N)
		}
		if rn.Sym == '\\' || (rn.Sym >= '0' && rn.Sym <= '9') {
			buf = append(buf, '\\')
		}
		buf = utf8.AppendRune(buf, rn.Sym)
	}
	return string(buf)
}

type sliceWriter []byte

func (b *sliceWriter) Write(p []byte) (int, error) {
	*b = append(*b, p...)
	return len(p), nil
}

const defaultOutputLimit = 1 << 30

type (
	// Option 配置流式 Decoder。
	Option func(*Decoder)
	// Decoder 是流式严格解码器，可接受任意字节切分。
	Decoder struct {
		w                io.Writer
		limit, written   int64
		checked          int // 输入字节被检查的总次数
		pos, off, symOff int
		phase            int // 0=起始 1=次数 2=转义 3=多字节
		digits, symbol   []byte
		count            int
		seen, hasPrev    bool
		prev             rune
		err              error
	}
)

// WithOutputLimit 限制解码产出的总字节数。
func WithOutputLimit(n int64) Option { return func(d *Decoder) { d.limit = n } }

// NewDecoder 创建流式解码器；输出上限默认 1<<30 字节。
func NewDecoder(w io.Writer, opts ...Option) *Decoder {
	d := &Decoder{w: w, limit: defaultOutputLimit}
	for _, o := range opts {
		o(d)
	}
	if d.limit <= 0 {
		d.limit = defaultOutputLimit
	}
	return d
}

// Decode 严格解码整段文本。
func Decode(t string) (string, error) {
	var out []byte
	d := NewDecoder((*sliceWriter)(&out))
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return string(out), d.Close()
}

func (d *Decoder) failAt(abs int, cause error) error {
	if d.err == nil {
		d.err = &DecodeError{Offset: abs, Err: cause}
	}
	return d.err
}

// Write 喂入任意切分的编码字节。
func (d *Decoder) Write(p []byte) (int, error) {
	d.checked += len(p)
	if d.err != nil {
		return len(p), d.err
	}
	base := d.pos
	for i := 0; i < len(p); i++ {
		c, at := p[i], base+i
		d.pos++
		if d.phase <= 1 && c >= '0' && c <= '9' {
			if d.phase == 0 {
				d.off = at
			}
			d.phase, d.digits = 1, append(d.digits, c)
			continue
		}
		if d.phase == 1 {
			if err := d.parseCount(); err != nil {
				return len(p), err
			}
			d.seen = true
		}
		d.symOff = at
		switch {
		case d.phase == 2 || (d.phase != 3 && c == '\\'):
			if d.phase != 2 {
				d.phase = 2
				continue
			}
			if !((c >= '0' && c <= '9') || c == '\\') {
				return len(p), d.failAt(at, ErrBadEscape)
			}
			if err := d.emit(rune(c)); err != nil {
				return len(p), err
			}
		case d.phase == 3 || c >= utf8.RuneSelf:
			if d.phase != 3 {
				d.phase, d.symbol = 3, append(d.symbol[:0], c)
				continue
			}
			d.symbol = append(d.symbol, c)
			r, size := utf8.DecodeRune(d.symbol)
			if r == utf8.RuneError {
				if size == 1 && len(d.symbol) < utf8.UTFMax {
					continue
				}
				return len(p), d.failAt(at, ErrInvalidUTF8)
			}
			if err := d.emit(r); err != nil {
				return len(p), err
			}
		default:
			if err := d.emit(rune(c)); err != nil {
				return len(p), err
			}
		}
		d.off, d.phase = at+1, 0
	}
	return len(p), d.err
}

func (d *Decoder) parseCount() error {
	if d.digits[0] == '0' {
		if len(d.digits) > 1 {
			return d.failAt(d.off, ErrCountLeadingZero)
		}
		return d.failAt(d.off, ErrCountZero)
	}
	v := 0
	for _, c := range d.digits {
		nv, ok := runs.AddDigit(v, c)
		if !ok {
			return d.failAt(d.off, ErrCountTooLarge)
		}
		v = nv
	}
	if v == 1 {
		return d.failAt(d.off, ErrCountOne)
	}
	d.count = v
	return nil
}

func (d *Decoder) emit(r rune) error {
	n := 1
	if d.seen {
		n = d.count
	}
	if d.hasPrev && d.prev == r {
		return d.failAt(d.symOff, ErrAdjacentSame)
	}
	width := utf8.RuneLen(r)
	if int64(n) > (d.limit-d.written)/int64(width) {
		return d.failAt(d.symOff, ErrOutputLimit)
	}
	var unit [utf8.UTFMax]byte
	w := utf8.EncodeRune(unit[:], r)
	buf := make([]byte, 0, 4096)
	for k := 0; k < n; k++ {
		if buf = append(buf, unit[:w]...); len(buf) >= 4096 {
			if _, err := d.w.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if _, err := d.w.Write(buf); err != nil {
			return err
		}
	}
	d.written += int64(n * w)
	d.prev, d.hasPrev, d.digits, d.seen = r, true, d.digits[:0], false
	return nil
}

// Close 结束输入并检查末尾完整性。
func (d *Decoder) Close() error {
	switch {
	case d.err != nil:
		return d.err
	case d.phase == 1:
		return d.failAt(d.off, ErrTrailingCount)
	case d.phase == 2:
		return d.failAt(d.symOff, ErrTrailingBackslash)
	case d.phase == 3:
		return d.failAt(d.symOff, ErrInvalidUTF8)
	}
	return nil
}

// Checked 返回输入字节被检查的总次数。
func (d *Decoder) Checked() int { return d.checked }
