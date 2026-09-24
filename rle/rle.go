// Package rle 实现带转义的文本游程编码（见 DESIGN.md 的格式定义）。
// 解码器是增量状态机：按任意字节切分喂入，结果与错误（含字节偏移）完全一致。
package rle

import (
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

var (
	// ErrCountOne：显式写出次数 1（必须省略）。
	ErrCountOne = errors.New("rle: explicit count of 1 is non-canonical")
	// ErrCountZero：次数为 0。
	ErrCountZero = errors.New("rle: run count is zero")
	// ErrLeadingZero：次数带前导零。
	ErrLeadingZero = errors.New("rle: count has leading zero")
	// ErrCountTooLarge：次数超过 runs.MaxCount。
	ErrCountTooLarge = errors.New("rle: run count exceeds maximum")
	// ErrAdjacentSame：相邻两个游程符号相同（未合并为最长游程）。
	ErrAdjacentSame = errors.New("rle: adjacent runs have the same symbol")
	// ErrBadEscape：反斜杠后既不是数字也不是反斜杠。
	ErrBadEscape = errors.New("rle: invalid escape after backslash")
	// ErrTrailingBackslash：输入以孤立反斜杠结尾。
	ErrTrailingBackslash = errors.New("rle: trailing backslash")
	// ErrMissingSymbol：只有次数没有符号。
	ErrMissingSymbol = errors.New("rle: count with no symbol")
	// ErrInvalidUTF8：非法 UTF-8 字节。
	ErrInvalidUTF8 = errors.New("rle: invalid UTF-8")
	// ErrOutputLimit：解码输出超过可配置字节上限。
	ErrOutputLimit = errors.New("rle: decoded output exceeds limit")
)

// DecodeError 携带底层哨兵错误与其在输入中的字节偏移（错误游程的起点）。
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return e.Err.Error() + " at offset " + strconv.Itoa(e.Offset) }
func (e *DecodeError) Unwrap() error { return e.Err }

const (
	stStart  byte = iota // 游程起点：期望数字或符号
	stDigits             // 已读到至少一位次数
	stEsc                // 刚读到反斜杠，等待被转义字符
	stRune               // 正在累计一个多字节符号
)

// Decoder 是流式严格解码器。字节检查次数记录在非导出字段 bytesChecked 中。
type Decoder struct {
	w            io.Writer
	outLimit     uint64
	outWritten   uint64
	bytesChecked uint64

	state     byte
	pos       int    // 已消费（检查）的字节数
	runStart  int    // 当前游程起点偏移
	count     uint64 // 已解析的次数；0 表示未写次数
	leadZero  bool
	overflow  bool
	esc       bool   // 下一个符号是否被反斜杠转义
	buf       []byte // 多字节符号的未满序列
	hasRune   bool
	haveLast  bool // 已成功输出过游程
	lastRune  rune
	pendRune  rune
	pendCount uint64
	pendEsc   bool
	closed    bool
	err       error
}

// NewDecoder 返回写入 w 的解码器。opts 可设置输出字节上限（WithOutputLimit）。
func NewDecoder(w io.Writer, opts ...func(*Decoder)) *Decoder {
	d := &Decoder{w: w, outLimit: uint64(math.MaxInt64)}
	for _, o := range opts {
		o(d)
	}
	return d
}

// WithOutputLimit 限制解码输出的总字节数，超限返回 ErrOutputLimit。
func WithOutputLimit(n uint64) func(*Decoder) {
	return func(d *Decoder) { d.outLimit = n }
}

// BytesChecked 返回输入字节被检查的总次数（每字节恰为一次，与切分无关）。
func (d *Decoder) BytesChecked() uint64 { return d.bytesChecked }

// Encode 返回 s 的规范游程编码。
func Encode(s string) string {
	var b strings.Builder
	runs.Each(s, func(r rune, n uint64) bool {
		if n >= 2 {
			b.WriteString(runs.FormatCount(n))
		}
		if r >= '0' && r <= '9' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		return true
	})
	return b.String()
}

// Decode 严格解码 t。默认输出上限为 MaxInt64 字节，可用 NewDecoder 配置更小上限。
func Decode(t string, opts ...func(*Decoder)) (string, error) {
	var b strings.Builder
	d := NewDecoder(&b, opts...)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (d *Decoder) fail(offset int, err error) error {
	if d.err == nil {
		d.err = &DecodeError{Offset: offset, Err: err}
	}
	return d.err
}

// Write 喂入下一段输入；出错后解码器不可继续使用。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for _, c := range p {
		d.bytesChecked++
		off := d.pos
		d.pos++
		if err := d.feed(c, off); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (d *Decoder) feed(c byte, off int) error {
	switch d.state {
	case stEsc:
		if c < '0' || c > '9' && c != '\\' {
			return d.fail(off-1, ErrBadEscape)
		}
		return d.symbol(rune(c), off)
	case stRune:
		d.buf = append(d.buf, c)
		if c&0xC0 != 0x80 {
			return d.fail(d.runStart, ErrInvalidUTF8)
		}
		if utf8.FullRune(d.buf) {
			r, _ := utf8.DecodeRune(d.buf)
			if r == utf8.RuneError {
				return d.fail(d.runStart, ErrInvalidUTF8)
			}
			d.buf = d.buf[:0]
			return d.symbol(r, d.runStart)
		}
		return nil
	}
	switch {
	case c >= '0' && c <= '9':
		if d.state == stStart {
			d.runStart = off
			d.state = stDigits
			if c == '0' {
				d.leadZero = true
			}
		} else if d.leadZero {
			return d.fail(d.runStart, ErrLeadingZero)
		}
		if d.overflow {
			return d.fail(d.runStart, ErrCountTooLarge)
		}
		n, ok := runs.AppendDigit(d.count, c)
		if !ok {
			d.overflow = true
			return d.fail(d.runStart, ErrCountTooLarge)
		}
		d.count = n
	case c == '\\':
		d.state, d.esc = stEsc, true
	default:
		if c < 0x80 {
			return d.symbol(rune(c), off)
		}
		if c&0xE0 == 0xC0 || c&0xF0 == 0xE0 || c&0xF8 == 0xF0 {
			d.runStart, d.state = off, stRune
			d.buf = append(d.buf[:0], c)
			if utf8.FullRune(d.buf) { // 两字节前导不可能，防御性处理
				return d.fail(off, ErrInvalidUTF8)
			}
			return nil
		}
		return d.fail(off, ErrInvalidUTF8)
	}
	return nil
}

// symbol 在一个完整符号（已应用转义语义）落定时进行规范校验并缓冲该游程。
func (d *Decoder) symbol(r rune, off int) error {
	if d.state == stDigits {
		switch {
		case d.count == 0:
			return d.fail(d.runStart, ErrCountZero)
		case d.leadZero:
			return d.fail(d.runStart, ErrLeadingZero)
		case d.overflow:
			return d.fail(d.runStart, ErrCountTooLarge)
		case d.count == 1:
			return d.fail(d.runStart, ErrCountOne)
		}
	}
	n := d.count
	if n == 0 {
		n = 1
	}
	d.pendRune, d.pendCount, d.pendEsc = r, n, d.esc
	d.hasRune = true

	if d.haveLast && d.lastRune == r {
		start := off
		if d.state == stDigits {
			start = d.runStart
		}
		return d.fail(start, ErrAdjacentSame)
	}
	if err := d.flushPending(); err != nil {
		return err
	}
	d.haveLast, d.lastRune = true, r
	d.state, d.count, d.leadZero, d.overflow, d.esc = stStart, 0, false, false, false
	return nil
}

func (d *Decoder) flushPending() error {
	if !d.hasRune {
		return nil
	}
	r, n, esc := d.pendRune, d.pendCount, d.pendEsc
	var one []byte
	if esc {
		one = []byte{byte(r)}
	} else {
		one = []byte(string(r))
	}
	total := uint64(len(one))
	remaining := d.outLimit - d.outWritten
	if total > remaining/n {
		return d.fail(d.runStart, ErrOutputLimit)
	}
	block := make([]byte, 0, 4096)
	for cap(block)-len(block) >= len(one) {
		block = append(block, one...)
	}
	copiesPerBlock := uint64(len(block) / len(one))
	for n > 0 {
		copies := copiesPerBlock
		if copies > n {
			copies = n
		}
		if _, err := d.w.Write(block[:int(copies)*len(one)]); err != nil {
			return d.fail(d.runStart, err)
		}
		n -= copies
	}
	d.outWritten += total * n
	d.hasRune = false
	return nil
}

// Close 校验流尾并写出最后一个游程。
func (d *Decoder) Close() error {
	if d.closed {
		return d.err
	}
	d.closed = true
	switch d.state {
	case stEsc:
		return d.fail(d.pos-1, ErrTrailingBackslash)
	case stRune:
		return d.fail(d.runStart, ErrInvalidUTF8)
	case stDigits:
		return d.fail(d.runStart, ErrMissingSymbol)
	}
	return d.flushPending()
}
