// Package rle 实现带转义的文本游程编码（格式与严格性论证见 DESIGN.md）。
package rle

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ontology/runs"
)

// 哨兵错误彼此可区分，可用 errors.Is 判定；DecodeError 另带字节偏移。
var (
	ErrCountOne     = errors.New("rle: explicit count of 1 is non-canonical")
	ErrCountZero    = errors.New("rle: count must not be zero")
	ErrLeadingZero  = errors.New("rle: count has leading zero")
	ErrAdjacentSame = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape    = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingBS   = errors.New("rle: trailing backslash")
	ErrTrailingNum  = errors.New("rle: trailing count without symbol")
	ErrInvalidUTF8  = errors.New("rle: invalid UTF-8 in symbol")
	ErrCountTooBig  = runs.ErrCountTooLarge
	ErrOutputLimit  = errors.New("rle: decoded output exceeds limit")
)

const DefaultLimit = 64 << 20 // Decode 与默认 Decoder 的输出字节上限

type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%s at offset %d", e.Err, e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

// Encode 返回 s 的规范 RLE 编码（s 须为合法 UTF-8）。
func Encode(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range runs.Split(s) {
		if r.Count >= 2 {
			b = runs.AppendCount(b, r.Count)
		}
		if r.Symbol == '\\' || '0' <= r.Symbol && r.Symbol <= '9' {
			b = append(b, '\\')
		}
		b = utf8.AppendRune(b, r.Symbol)
	}
	return string(b)
}

// Decode 严格解码 t；DecodeLimit 使用给定输出字节上限。
func Decode(t string) (string, error) { return DecodeLimit(t, DefaultLimit) }

func DecodeLimit(t string, limit int) (string, error) {
	d := NewDecoder(WithLimit(limit))
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

type run struct {
	symbol rune
	count  int
}

// Decoder 是流式严格解码器。它保留一个待发游程以检测相邻同符号，
// 故完整结果在 Close 后才可得。
type Decoder struct {
	limit    int
	out      []byte
	buf      []byte // 尚未解析、可能跨切分不完整的输入
	examined int    // 非导出计数器：输入字节被检查的总次数
	pending  *run
	closed   bool
	closeErr error
}

type Option func(*Decoder)

func WithLimit(n int) Option { return func(d *Decoder) { d.limit = n } }

func NewDecoder(opts ...Option) *Decoder {
	d := &Decoder{limit: DefaultLimit}
	for _, o := range opts {
		o(d)
	}
	return d
}

func (d *Decoder) String() string { return string(d.out) }

// Write 喂入一段编码；每个字节自进入解码器起只被检查一次。
func (d *Decoder) Write(p []byte) (int, error) {
	n := len(p)
	d.examined += n
	if d.closed {
		return n, &DecodeError{d.examined - n, errors.New("rle: decoder closed")}
	}
	d.buf = append(d.buf, p...)
	return n, d.parse(false)
}

func (d *Decoder) Close() error {
	if d.closed {
		return d.closeErr
	}
	d.closed = true
	if err := d.parse(true); err != nil {
		d.closeErr = err
		return err
	}
	if d.pending != nil {
		if err := d.emit(d.pending, d.examined); err != nil {
			d.closeErr = err
			return err
		}
		d.pending = nil
	}
	return nil
}

func (d *Decoder) base() int { return d.examined - len(d.buf) }

// parse 反复解析完整游程；atEOF 时末尾不完整状态也要报错。
func (d *Decoder) parse(atEOF bool) error {
	i := 0
	for i < len(d.buf) {
		start := i
		count, digitLen, err := runs.ParseCount(string(d.buf[i:]))
		if err != nil {
			return d.fail(d.base()+start, ErrCountTooBig)
		}
		if digitLen > 0 {
			switch {
			case count == 0:
				return d.fail(d.base()+start, ErrCountZero)
			case count == 1:
				return d.fail(d.base()+start, ErrCountOne)
			case d.buf[i] == '0':
				return d.fail(d.base()+start, ErrLeadingZero)
			}
		}
		i += digitLen
		if i >= len(d.buf) { // 缺少符号
			if atEOF {
				return d.fail(d.base()+start, ErrTrailingNum)
			}
			return d.savePartial(i)
		}
		var symbol rune
		if d.buf[i] == '\\' {
			if i+1 >= len(d.buf) {
				if atEOF {
					return d.fail(d.base()+i, ErrTrailingBS)
				}
				return d.savePartial(i)
			}
			c := d.buf[i+1]
			if !(('0' <= c && c <= '9') || c == '\\') {
				return d.fail(d.base()+i, ErrBadEscape)
			}
			symbol, i = rune(c), i+2
		} else {
			r, size := utf8.DecodeRune(d.buf[i:])
			if r == utf8.RuneError {
				if atEOF || size == 1 && isDefiniteInvalid(d.buf[i]) {
					return d.fail(d.base()+i, ErrInvalidUTF8)
				}
				return d.savePartial(i)
			}
			symbol, i = r, i+size
		}
		if d.pending != nil && d.pending.symbol == symbol {
			return d.fail(d.base()+start, ErrAdjacentSame)
		}
		if d.pending != nil {
			if err := d.emit(d.pending, d.base()+start); err != nil {
				return err
			}
		}
		d.pending = &run{symbol, count}
	}
	return d.savePartial(i)
}

func (d *Decoder) savePartial(i int) error {
	if i > 0 {
		d.buf = append(d.buf[:0], d.buf[i:]...)
	}
	return nil
}

func (d *Decoder) fail(offset int, err error) error {
	return &DecodeError{Offset: offset, Err: err}
}

// emit 在产出字节前做预算检查，绝不按次数预分配。
func (d *Decoder) emit(r *run, runStartOffset int) error {
	width := utf8.RuneLen(r.symbol)
	if width < 0 {
		width = 1
	}
	if r.count > (d.limit-len(d.out))/width {
		return &DecodeError{Offset: runStartOffset, Err: ErrOutputLimit}
	}
	var enc [4]byte
	n := utf8.EncodeRune(enc[:], r.symbol)
	for j := 0; j < r.count; j++ {
		d.out = append(d.out, enc[:n]...)
	}
	return nil
}

func isDefiniteInvalid(b byte) bool { return b&0xC0 == 0x80 || b >= 0xF8 }
