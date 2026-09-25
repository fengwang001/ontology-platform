package rle

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// 哨兵错误：严格解码拒绝的各类输入，彼此可用 errors.Is 区分。
var (
	ErrCountOne       = errors.New("rle: explicit repeat count 1 is non-canonical")
	ErrCountZero      = errors.New("rle: repeat count 0")
	ErrLeadingZero    = errors.New("rle: leading zero in repeat count")
	ErrAdjacentSymbol = errors.New("rle: adjacent runs share one symbol")
	ErrBadEscape      = errors.New("rle: backslash must precede a digit or backslash")
	ErrTrailingSlash  = errors.New("rle: dangling backslash")
	ErrMissingSymbol  = errors.New("rle: count without symbol at end")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8")
	ErrOutputLimit    = errors.New("rle: decoded output exceeds limit")
	ErrCountTooLarge  = runs.ErrCountTooLarge
)

// Error 携带字节偏移的解码错误。
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func (d *Decoder) fail(off int, err error) error {
	if d.err == nil {
		d.err = &Error{Offset: off, Err: err}
	}
	return d.err
}

// Encode 输出规范 RLE：最长游程、次数 1 省略、数字与反斜杠转义。
func Encode(s string) string {
	rs := []rune(s)
	out := make([]byte, 0, len(s))
	for _, rn := range runs.Split(rs) {
		if rn.Count > 1 {
			out = runs.AppendCount(out, rn.Count)
		}
		if rn.Symbol == '\\' || (rn.Symbol >= '0' && rn.Symbol <= '9') {
			out = append(out, '\\')
		}
		out = append(out, string(rn.Symbol)...)
	}
	return string(out)
}

// Decode 严格解码；非法或非规范输入返回带偏移的 *Error。
func Decode(t string) (string, error) {
	var sb strings.Builder
	d := NewDecoder(&sb)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// Option 配置解码器。
type Option func(*config)

type config struct{ maxOutput int64 }

// WithMaxOutput 设置解码输出总字节上限；n<=0 表示不限。
func WithMaxOutput(n int64) Option { return func(c *config) { c.maxOutput = n } }

// Decoder 是流式严格解码器。
type Decoder struct {
	w           io.Writer
	maxOutput   int64
	outSize     int64
	digits      runs.DigitBuffer
	digitState  byte // 0=无数字 1=恰好一个 0 2=正常（含前导零后的数字）
	prevSymbol  rune
	havePrev    bool
	curSymbol   rune
	curCount    int64
	leadingZero bool
	escape      bool
	need        int
	runeLen     int
	checked     int64
	runStart    int
	offset      int
	err         error
}

// NewDecoder 创建流式解码器，输出写入 w。
func NewDecoder(w io.Writer, opts ...Option) *Decoder {
	c := config{maxOutput: 64 << 20}
	for _, o := range opts {
		o(&c)
	}
	return &Decoder{w: w, maxOutput: c.maxOutput, prevSymbol: -1, curSymbol: -1, runStart: -1}
}

// CheckedBytes 返回输入字节被检查的总次数。
func (d *Decoder) CheckedBytes() int64 { return d.checked }

// Write 喂入一段编码字节；每个字节恰被检查一次。
func (d *Decoder) Write(p []byte) (int, error) {
	for i, b := range p {
		d.checked++
		if d.err == nil {
			d.feed(b)
		}
		if d.err != nil {
			return i + 1, d.err
		}
	}
	return len(p), nil
}

func (d *Decoder) startRun() {
	if d.runStart < 0 {
		d.runStart = d.offset
	}
}

func (d *Decoder) feed(b byte) {
	d.offset++
	off := d.offset - 1
	if d.escape {
		d.escape = false
		if b == '\\' || b >= '0' && b <= '9' {
			d.symbol(rune(b), off)
			return
		}
		d.fail(off-1, ErrBadEscape)
		return
	}
	if d.need > 0 {
		if b&0xC0 != 0x80 {
			d.fail(d.runStart, ErrInvalidUTF8)
			return
		}
		d.curSymbol = d.curSymbol<<6 | rune(b&0x3F)
		d.need--
		if d.need == 0 {
			r := d.curSymbol
			overlong := (d.runeLen == 2 && r < 0x80) ||
				(d.runeLen == 3 && r < 0x800) ||
				(d.runeLen == 4 && r < 0x10000)
			if !utf8.ValidRune(r) || overlong {
				d.fail(d.runStart, ErrInvalidUTF8)
				return
			}
			d.symbol(r, off)
		}
		return
	}
	switch {
	case b >= '0' && b <= '9':
		d.startRun()
		if d.digitState == 1 && b != '0' {
			d.fail(d.runStart, ErrLeadingZero)
			return
		}
		if d.digitState == 0 {
			if b == '0' {
				d.digitState = 1
			} else {
				d.digitState = 2
			}
		} else if d.digitState == 1 && b == '0' {
			d.fail(d.runStart, ErrLeadingZero)
			return
		}
		if err := d.digits.Add(b); err != nil {
			d.fail(off, err)
			return
		}
	case b == '\\':
		d.startRun()
		d.escape = true
	default:
		d.startRun()
		d.startRune(b, off)
	}
}

func (d *Decoder) startRune(b byte, off int) {
	switch {
	case b < 0x80:
		d.symbol(rune(b), off)
	case b&0xE0 == 0xC0 && b >= 0xC2:
		d.need, d.runeLen = 1, 2
		d.curSymbol = rune(b & 0x1F)
	case b&0xF0 == 0xE0 && b < 0xF0:
		d.need, d.runeLen = 2, 3
		d.curSymbol = rune(b & 0x0F)
	case b&0xF8 == 0xF0 && b < 0xF5:
		d.need, d.runeLen = 3, 4
		d.curSymbol = rune(b & 0x07)
	default:
		d.fail(off, ErrInvalidUTF8)
	}
}

func (d *Decoder) symbol(r rune, off int) {
	if d.digitState == 1 {
		d.fail(d.runStart, ErrCountZero)
		return
	}
	n := int64(1)
	if !d.digits.Empty() {
		n = d.digits.Count()
	}
	if n == 1 && !d.digits.Empty() {
		d.fail(d.runStart, ErrCountOne)
		return
	}
	if d.havePrev && d.prevSymbol == r {
		d.fail(d.runStart, ErrAdjacentSymbol)
		return
	}
	d.curSymbol, d.curCount = r, n
	if err := d.flush(); err != nil {
		d.fail(d.runStart, err)
		return
	}
	d.prevSymbol, d.havePrev = r, true
	d.digits = runs.DigitBuffer{}
	d.digitState, d.runStart = 0, -1
}

func (d *Decoder) flush() error {
	n, r := d.curCount, d.curSymbol
	width := int64(utf8.RuneLen(r))
	if n > (1<<63-1-d.outSize)/width {
		return ErrOutputLimit
	}
	size := n * width
	if d.maxOutput > 0 && d.outSize+size > d.maxOutput {
		return ErrOutputLimit
	}
	var unit [4]byte
	w := utf8.EncodeRune(unit[:], r)
	chunk := unit[:w]
	for n > 0 {
		c := n
		if c > 4096 {
			c = 4096
		}
		for k := int64(0); k < c; k++ {
			if _, err := d.w.Write(chunk); err != nil {
				return err
			}
		}
		n -= c
	}
	d.outSize += size
	return nil
}

// Close 结束解码，报告残缺游程等末尾错误。
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if d.escape {
		return d.fail(d.offset-1, ErrTrailingSlash)
	}
	if d.need > 0 {
		return d.fail(d.runStart, ErrInvalidUTF8)
	}
	if d.runStart >= 0 {
		return d.fail(d.runStart, ErrMissingSymbol)
	}
	return nil
}
