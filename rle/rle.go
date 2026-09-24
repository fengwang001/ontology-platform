// Package rle 实现带转义的文本游程编码编解码器，依赖 runs 包。
package rle

import (
	"errors"
	"math"
	"unicode/utf8"

	"ontology/runs"
)

// DefaultLimit 是默认输出字节上限（256 MiB）。
const DefaultLimit = 256 << 20

var (
	ErrCountOne      = errors.New("rle: explicit count of 1 is non-canonical")
	ErrCountZero     = errors.New("rle: run count must be positive")
	ErrLeadingZero   = errors.New("rle: count has a leading zero")
	ErrAdjacent      = errors.New("rle: adjacent runs with the same symbol")
	ErrBadEscape     = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash = errors.New("rle: trailing backslash")
	ErrDanglingCount = errors.New("rle: count without a following symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8 in symbol")
	ErrOutputLimit   = errors.New("rle: output byte limit exceeded")
)

// DecodeError 携带哨兵错误与输入字节偏移；用 errors.Is 判定具体类别。
type DecodeError struct {
	Err error
	Off int
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

const (
	stStart = iota // 游程起点
	stCount        // 正在读次数
	stSlash        // 刚读完 '\'
	stUTF8         // 正在拼跨切分的多字节符号
)

func escaped(c rune) bool { return c == '\\' || '0' <= c && c <= '9' }

// Encode 返回 s 的规范编码。
func Encode(s string) string {
	out := make([]byte, 0, len(s))
	runs.Split(s, func(r runs.Run) bool {
		if r.Count >= 2 {
			out = runs.AppendCount(out, r.Count)
		}
		if escaped(r.Symbol) {
			out = append(out, '\\')
		}
		var b [utf8.UTFMax]byte
		out = append(out, b[:utf8.EncodeRune(b[:], r.Symbol)]...)
		return true
	})
	return string(out)
}

// Decode 严格解码 t，输出上限为 DefaultLimit。
func Decode(t string) (string, error) {
	d := NewDecoder(DefaultLimit)
	if err := d.Write([]byte(t)); err != nil {
	return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

// Decoder 是流式严格解码器，每个输入字节只检查一次；实现 io.WriteCloser。
type Decoder struct {
	limit int

	state   int
	runOff  int
	digits  int
	leadZero bool
	count   int
	last    rune
	haveLast bool

	pend    []byte
	pendOff int
	need    int

	out          []byte
	BytesChecked int
	err          error
}

// NewDecoder 创建输出字节上限为 limit 的解码器；limit <= 0 用 DefaultLimit。
func NewDecoder(limit int) *Decoder {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Decoder{limit: limit, last: utf8.RuneError}
}

func (d *Decoder) fail(off int, err error) error {
	if d.err == nil {
		d.err = &DecodeError{Err: err, Off: off}
	}
	return d.err
}

// Write 追加解码输入；出错后 Decoder 永久失效。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	d.BytesChecked += len(p)
	base := d.BytesChecked - len(p)
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch d.state {
		case stUTF8:
			if b < 0x80 || b > 0xBF {
				return len(p), d.fail(d.pendOff, ErrInvalidUTF8)
			}
			d.pend = append(d.pend, b)
			if len(d.pend) == d.need {
				r, _ := utf8.DecodeRune(d.pend)
				if r == utf8.RuneError {
					return len(p), d.fail(d.pendOff, ErrInvalidUTF8)
				}
				if err := d.complete(d.pendOff, r); err != nil {
					return len(p), err
				}
			}
		case stSlash:
			if b != '\\' && (b < '0' || b > '9') {
				return len(p), d.fail(base+i-1, ErrBadEscape)
			}
			if err := d.complete(base+i, rune(b)); err != nil {
				return len(p), err
			}
		default:
			switch {
			case '0' <= b && b <= '9':
				if d.digits == 0 {
					d.runOff, d.leadZero = base+i, b == '0'
				}
				v := int(b - '0')
				if d.count > (runs.MaxCount-v)/10 {
					return len(p), d.fail(d.runOff, runs.ErrCountTooLarge{})
				}
				d.count, d.digits, d.state = d.count*10+v, d.digits+1, stCount
			case b == '\\':
				if d.digits == 0 {
					d.runOff = base + i
				}
				d.state = stSlash
			case b < utf8.RuneSelf:
				if err := d.complete(base+i, rune(b)); err != nil {
					return len(p), err
				}
			default:
				r, size := utf8.DecodeRune(p[i:])
				if r == utf8.RuneError {
					if i+expect(b) <= len(p) {
						return len(p), d.fail(base+i, ErrInvalidUTF8)
					}
					d.pendOff, d.need = base+i, expect(b)
					d.pend = append(d.pend[:0], p[i:]...)
					d.state = stUTF8
					i = len(p)
				} else if err := d.complete(base+i, r); err != nil {
					return len(p), err
				} else {
					i += size - 1
				}
			}
		}
	}
	if d.state == stUTF8 && len(d.pend) == d.need {
		r, _ := utf8.DecodeRune(d.pend)
		if r == utf8.RuneError {
			return len(p), d.fail(d.pendOff, ErrInvalidUTF8)
		}
		if err := d.complete(d.pendOff, r); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

func expect(b byte) int {
	switch {
	case b < 0xC2:
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

// complete 处理一个已完整读出的符号。
func (d *Decoder) complete(off int, sym rune) error {
	if d.digits > 0 {
		switch {
		case d.leadZero:
			return d.fail(d.runOff, ErrLeadingZero)
		case d.count == 0:
			return d.fail(d.runOff, ErrCountZero)
		case d.count == 1:
			return d.fail(d.runOff, ErrCountOne)
		}
	}
	if d.haveLast && sym == d.last {
		return d.fail(d.runOffOr(off), ErrAdjacent)
	}
	n := 1
	if d.digits > 0 {
		n = d.count
	}
	width := utf8.RuneLen(sym)
	if width < 0 || n > math.MaxInt/width || n*width > d.limit {
		return d.fail(d.runOffOr(off), ErrOutputLimit)
	}
	d.limit -= n * width
	var buf [utf8.UTFMax]byte
	chunk := buf[:utf8.EncodeRune(buf[:], sym)]
	for n > 0 {
		k := n
		if k > 64 {
			k = 64
		}
		for j := 0; j < k; j++ {
			d.out = append(d.out, chunk...)
		}
		n -= k
	}
	d.last, d.haveLast = sym, true
	d.state, d.digits, d.leadZero, d.count = stStart, 0, false, 0
	d.pend, d.need = d.pend[:0], 0
	return nil
}

func (d *Decoder) runOffOr(off int) int {
	if d.digits > 0 {
		return d.runOff
	}
	return off
}

// Close 校验输入在游程边界结束。
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch {
	case d.state == stUTF8:
		return d.fail(d.pendOff, ErrInvalidUTF8)
	case d.state == stSlash:
		return d.fail(d.BytesChecked-1, ErrTrailingSlash)
	case d.state == stCount:
		return d.fail(d.runOff, ErrDanglingCount)
	}
	return nil
}

// String 返回目前为止解码出的文本。
func (d *Decoder) String() string { return string(d.out) }
