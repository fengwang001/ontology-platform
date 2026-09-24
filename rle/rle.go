// Package rle 实现带转义的文本游程编码：Encode 与严格流式 Decode。
package rle

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/runs"
)

// 哨兵错误彼此可区分；均包装在带字节偏移的 DecodeError 中。
var (
	ErrCountOne       = errors.New("rle: explicit count of 1 is non-canonical")
	ErrCountZero      = errors.New("rle: run count is zero")
	ErrLeadingZero    = errors.New("rle: count has leading zero")
	ErrAdjacentSymbol = errors.New("rle: adjacent runs share the same symbol")
	ErrBadEscape      = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash  = errors.New("rle: trailing backslash")
	ErrTrailingCount  = errors.New("rle: count not followed by a symbol")
	ErrInvalidUTF8    = errors.New("rle: invalid UTF-8 in symbol")
	ErrCountTooLarge  = errors.New("rle: run count exceeds MaxInt")
	ErrOutputLimit    = errors.New("rle: decoded output exceeds configured limit")
)

// DecodeError 携带底层哨兵错误与出错 token 在总输入中的起始字节偏移。
type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// DefaultOutputLimit 是 Decode 的默认输出上限（64 MiB）。
const DefaultOutputLimit = 64 << 20

// Encode 返回 s 的规范游程编码。
func Encode(s string) string {
	var b strings.Builder
	for _, rn := range runs.Split(s) {
		if rn.Count > 1 {
			b.WriteString(strconv.Itoa(rn.Count))
		}
		if (rn.Symbol >= '0' && rn.Symbol <= '9') || rn.Symbol == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(rn.Symbol)
	}
	return b.String()
}

// Decode 严格解码 t；任何输出不得超过 DefaultOutputLimit。
func Decode(t string) (string, error) {
	var sb strings.Builder
	d := NewDecoder(&sb, DefaultOutputLimit)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return sb.String(), nil
}

const (
	stRun    = iota // 期待游程起点
	stZero          // 已读单个 '0'
	stNum           // 已读 >=1 位数字（首位非 0）
	stEscape        // 读到 '\\'
	stUTF8          // 正在累积多字节符号
)

// Decoder 是增量、可跨任意字节切分的严格解码器。
type Decoder struct {
	w         io.Writer
	limit     int
	checked   int // 输入字节被检查的总次数（每字节恰好一次）
	off       int // 下一个字节在总输入中的偏移
	state     int
	count     int
	numOff    int // 当前次数起始偏移
	tokOff    int // 当前符号起始偏移
	pend      []byte
	pendN     int
	haveCount bool
	last      rune
	hasLast   bool
	utf       []byte
	need      int
	failed    bool
}

// NewDecoder 创建输出上限为 limit 字节（<=0 表示 DefaultOutputLimit）的解码器。
func NewDecoder(w io.Writer, limit int) *Decoder {
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	return &Decoder{w: w, limit: limit}
}

// Checked 返回截至目前被检查的输入字节总数。
func (d *Decoder) Checked() int { return d.checked }

func (d *Decoder) fail(off int, err error) error {
	d.failed = true
	return &DecodeError{Offset: off, Err: err}
}

// Write 喂入任意长度的输入片段；非法时返回带偏移的错误，之后解码器不可用。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.failed {
		return 0, errors.New("rle: decoder already failed")
	}
	i := 0
	for i < len(p) {
		d.checked++
		c := p[i]
		switch d.state {
		case stRun, stNum:
			if runs.IsCountDigit(c) {
				if d.state == stRun {
					d.numOff, d.count = d.off, 0
					d.haveCount = true
					if c == '0' {
						d.state = stZero
						break
					}
					d.state = stNum
				}
				n, ok := runs.AppendDigit(d.count, c)
				if !ok {
					return i + 1, d.fail(d.numOff, ErrCountTooLarge)
				}
				d.count = n
			} else {
				if err := d.startSymbol(c); err != nil {
					return i + 1, err
				}
				if d.state == stEscape {
					// 转义符本身消耗一个字节，下一字节才是被转义字符。
					i++
					d.off++
					continue
				}
				if d.state == stUTF8 {
					i++
					d.off++
					for i < len(p) && len(d.utf) < d.need {
						d.checked++
						d.utf = append(d.utf, p[i])
						i++
						d.off++
					}
					if len(d.utf) < d.need {
						return i, nil // 等待更多片段
					}
					r, sz := utf8.DecodeRune(d.utf)
					if r == utf8.RuneError && sz != len(d.utf) {
						return i, d.fail(d.tokOff, ErrInvalidUTF8)
					}
					if err := d.finish(r); err != nil {
						return i, err
					}
					continue
				}
			}
		case stZero:
			if runs.IsCountDigit(c) {
				return i + 1, d.fail(d.numOff, ErrLeadingZero)
			}
			return i + 1, d.fail(d.numOff, ErrCountZero)
		case stEscape:
			switch {
			case c == '\\' || runs.IsCountDigit(c):
				if err := d.finish(rune(c)); err != nil {
					return i + 1, err
				}
			default:
				return i + 1, d.fail(d.tokOff, ErrBadEscape)
			}
		case stUTF8:
			// 续接之前跨片段的多字节符号。
			for i < len(p) && len(d.utf) < d.need {
				d.checked++
				d.utf = append(d.utf, p[i])
				i++
				d.off++
			}
			if len(d.utf) < d.need {
				return i, nil
			}
			r, sz := utf8.DecodeRune(d.utf)
			if r == utf8.RuneError && sz != len(d.utf) {
				return i, d.fail(d.tokOff, ErrInvalidUTF8)
			}
			if err := d.finish(r); err != nil {
				return i, err
			}
			continue
		}
		i++
		d.off++
	}
	return len(p), nil
}

// startSymbol 处理一个符号的首字节（此时已确认不是次数数字）。
func (d *Decoder) startSymbol(c byte) error {
	d.tokOff = d.off
	switch {
	case c == '\\':
		d.state = stEscape
	case c < 0x80:
		return d.finish(rune(c))
	default:
		d.need = leadLen(c)
		d.utf = append(d.utf[:0], c)
		d.state = stUTF8
	}
	return nil
}

// leadLen 报告 UTF-8 首字节声明的续字节数；非法首字节返回 -1。
func leadLen(c byte) int {
	switch {
	case c < 0x80:
		return 1
	case c < 0xC0:
		return -1
	case c < 0xE0:
		return 2
	case c < 0xF0:
		return 3
	case c < 0xF8:
		return 4
	default:
		return -1
	}
}

// finish 在一个符号解析完成时校验计数/相邻性并暂存该游程（延迟提交）。
func (d *Decoder) finish(r rune) error {
	count := d.count
	if count == 0 && !d.haveCount {
		count = 1
	}
	explicitOne := d.haveCount && count == 1
	d.count, d.haveCount, d.state = 0, false, stRun
	if explicitOne {
		return d.fail(d.tokOff, ErrCountOne) // 次数 1 附着在该符号上
	}
	if d.hasLast && d.last == r {
		return d.fail(d.tokOff, ErrAdjacentSymbol) // 指向下一个同符号
	}
	if err := d.flush(); err != nil {
		return err
	}
	d.last, d.hasLast = r, true
	d.pend = append(d.pend[:0], string(r)...)
	d.pendN = count
	return nil
}

// flush 提交暂存游程；按累计输出字节做配额检查（乘法防溢出）。
func (d *Decoder) flush() error {
	if d.pendN == 0 {
		return nil
	}
	w := len(d.pend)
	if d.pendN > d.limit/w {
		return d.fail(d.tokOff, ErrOutputLimit)
	}
	total := d.pendN * w
	out := make([]byte, 0, total)
	for i := 0; i < d.pendN; i++ {
		out = append(out, d.pend...)
	}
	if _, err := d.w.Write(out); err != nil {
		return err
	}
	d.pendN = 0
	d.limit -= total
	return nil
}

// Close 收尾：拒绝不完整状态，提交最后一个游程。
func (d *Decoder) Close() error {
	if d.failed {
		return errors.New("rle: decoder already failed")
	}
	switch d.state {
	case stEscape:
		return d.fail(d.tokOff, ErrTrailingSlash)
	case stZero:
		return d.fail(d.numOff, ErrCountZero)
	case stNum:
		return d.fail(d.numOff, ErrTrailingCount)
	case stUTF8:
		return d.fail(d.tokOff, ErrInvalidUTF8)
	}
	return d.flush()
}
