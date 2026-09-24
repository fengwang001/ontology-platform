// Package rle 实现带转义的文本游程编码编解码。
//
// 编码形如 [次数]符号 首尾相接；符号为 ASCII 数字或反斜杠时以反斜杠转义。
// 解码为严格解码：仅接受规范形式，保证 Encode(Decode(t)) == t。
package rle

import (
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// DecodeError 携带错误种类与输入字节偏移。
type DecodeError struct {
	Err    error
	Offset int64
}

func (e *DecodeError) Error() string { return e.Err.Error() + " at offset " + strconv.FormatInt(e.Offset, 10) }
func (e *DecodeError) Unwrap() error { return e.Err }

// 哨兵错误：彼此可判定（errors.Is）。
var (
	ErrCountOne      = errors.New("rle: explicit count of 1 is non-canonical")
	ErrCountZero     = errors.New("rle: run count is zero")
	ErrLeadingZero   = errors.New("rle: count has leading zero")
	ErrSameAdjacent  = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape     = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash = errors.New("rle: trailing backslash without symbol")
	ErrCountNoSymbol = errors.New("rle: count without a following symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8 in symbol")
	ErrOutputLimit   = errors.New("rle: output byte limit exceeded")
)

// Encode 返回 s 的规范 RLE 文本。
func Encode(s string) string {
	var b strings.Builder
	runs.Split(s, func(r rune, n int64) {
		if n > 1 {
			b.WriteString(strconv.FormatInt(n, 10))
		}
		if r == '\\' || r >= '0' && r <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	})
	return b.String()
}

// Decode 严格解码 t（默认输出上限 1 GiB）。
func Decode(t string) (string, error) {
	var b strings.Builder
	d := NewDecoder(&b, DefaultOutputLimit)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// DefaultOutputLimit 是 Decode 与 Decoder 的默认输出字节上限。
const DefaultOutputLimit int64 = 1 << 30

// Decoder 是流式严格解码器，实现 io.WriteCloser。
// examined 为非导出计数器，记录输入字节被检查的总次数。
type Decoder struct {
	w        io.Writer
	limit    int64
	out      int64
	examined int64
	closed   bool
	failed   bool

	// 当前游程
	count   runs.Count
	first0  int64 // 前导 0 的偏移；-1 表示无
	runOff  int64 // 当前游程起始偏移
	escaped bool

	// 多字节符号
	symbol []byte // 已收集的符号字节（可能不完整）
	symOff int64
	want   int   // 该符号还期望的字节数（0 表示未在读符号）

	// 已完成、待与下一游程比较/写出的游程
	pending     bool
	pendRune    rune
	pendCount   *big.Int
	pendOff     int64
	haveLast    bool
	lastRune    rune
}

// NewDecoder 创建输出写入 w、输出上限为 limit（<=0 用默认值）的解码器。
func NewDecoder(w io.Writer, limit int64) *Decoder {
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	return &Decoder{w: w, limit: limit, first0: -1, pendOff: -1}
}

// Examined 返回输入字节被检查的总次数。
func (d *Decoder) Examined() int64 { return d.examined }

func (d *Decoder) fail(kind error, off int64) error {
	d.failed = true
	return &DecodeError{Err: kind, Offset: off}
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed || d.failed {
		return len(p), nil // 关闭或已失败后丢弃，保持流式幂等
	}
	for i, b := range p {
		if err := d.feed(b); err != nil {
			d.failed = true
			return i, err
		}
		d.examined++
	}
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.failed {
		return nil
	}
	d.closed = true
	off := d.examined
	if d.escaped {
		return d.fail(ErrTrailingSlash, off)
	}
	if d.want > 0 {
		return d.fail(ErrInvalidUTF8, d.symOff)
	}
	if d.count.Len() > 0 {
		return d.fail(ErrCountNoSymbol, d.runOff)
	}
	if d.pending {
		return d.emit(d.pendRune, d.pendCount, d.pendOff)
	}
	return nil
}

// feed 消费一个字节（偏移由 examined 隐式给出）；返回错误时该字节尚未计入 examined。
func (d *Decoder) feed(b byte) error {
	off := d.examined

	// 正在收集多字节符号
	if d.want > 0 {
		if b&0xC0 != 0x80 {
			return d.fail(ErrInvalidUTF8, d.symOff)
		}
		d.symbol = append(d.symbol, b)
		d.want--
		if d.want == 0 {
			return d.finishSymbol(d.symbol)
		}
		return nil
	}

	if d.escaped {
		d.escaped = false
		if (b >= '0' && b <= '9') || b == '\\' {
			return d.beginOneByteSymbol(b)
		}
		return d.fail(ErrBadEscape, off)
	}

	switch {
	case b >= '0' && b <= '9':
		if d.count.Len() == 0 {
			d.runOff = off
			if b == '0' {
				d.first0 = off
			}
		} else if d.first0 >= 0 {
			return d.fail(ErrLeadingZero, d.first0)
		}
		d.count.AddDigit(b)
		return nil
	case b == '\\':
		if d.count.Len() == 0 {
			d.runOff = off
		}
		d.escaped = true
		return nil
	default:
		if d.count.Len() == 0 {
			d.runOff = off
		}
		return d.beginUTF8Symbol(b, off)
	}
}

func (d *Decoder) beginOneByteSymbol(b byte) error {
	r := rune(b)
	return d.acceptSymbol(r, []byte{b})
}

func (d *Decoder) beginUTF8Symbol(first byte, off int64) error {
	switch {
	case first < 0x80:
		return d.acceptSymbol(rune(first), []byte{first})
	case first&0xE0 == 0xC0:
		d.want = 1
	case first&0xF0 == 0xE0:
		d.want = 2
	case first&0xF8 == 0xF0:
		d.want = 3
	default:
		return d.fail(ErrInvalidUTF8, off)
	}
	d.symbol = append(d.symbol[:0], first)
	d.symOff = off
	return nil
}

func (d *Decoder) finishSymbol(raw []byte) error {
	r, size := utf8.DecodeRune(raw)
	if r == utf8.RuneError && size == 1 {
		return d.fail(ErrInvalidUTF8, d.symOff)
	}
	return d.acceptSymbol(r, raw)
}

// acceptSymbol 完成当前游程：做次数合法性与相邻同符号校验，然后挂为 pending。
func (d *Decoder) acceptSymbol(r rune, raw []byte) error {
	n := d.count.Value()
	switch {
	case d.count.IsZero():
		return d.fail(ErrCountZero, d.runOff)
	case d.count.IsOne():
		return d.fail(ErrCountOne, d.runOff)
	case d.haveLast && d.lastRune == r:
		return d.fail(ErrSameAdjacent, d.runOff)
	}
	if d.pending {
		if err := d.emit(d.pendRune, d.pendCount, d.pendOff); err != nil {
			return err
		}
	}
	d.pending, d.pendRune, d.pendCount, d.pendOff = true, r, n, d.runOff
	d.haveLast, d.lastRune = true, r
	d.count = runs.Count{}
	d.first0 = -1
	return nil
}

// emit 分块写出 r 共 n 次，不按 n 分配内存，并校验输出字节上限。
func (d *Decoder) emit(r rune, n *big.Int, off int64) error {
	rs := utf8.RuneLen(r)
	total := new(big.Int).Mul(n, big.NewInt(int64(rs)))
	if total.IsInt64() {
		v := total.Int64()
		if d.out+v > d.limit {
			return d.fail(ErrOutputLimit, off)
		}
		d.out += v
	} else {
		return d.fail(ErrOutputLimit, off)
	}

	buf := make([]byte, 0, 256)
	remaining := new(big.Int).Set(n)
	chunk := big.NewInt(32) // 256/rs>=32，足以填满缓冲
	for remaining.Sign() > 0 {
		take := new(big.Int).Set(chunk)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		k := int(take.Int64())
		for i := 0; i < k; i++ {
			buf = utf8.AppendRune(buf, r)
		}
		if _, err := d.w.Write(buf); err != nil {
			return err
		}
		buf = buf[:0]
		remaining.Sub(remaining, take)
}
	d.pending = false
	return nil
}
