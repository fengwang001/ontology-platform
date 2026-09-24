// Package jstr 实现单个 JSON 字符串字面量（含两端引号）的严格编解码器。
// 它只处理两个双引号之间的内容，不解析对象或数组。
package jstr

import (
	"errors"
	"fmt"

	"ontology/esc"
)

// 五类可判定的解码错误，彼此可通过 errors.Is 区分。
var (
	ErrControl      = errors.New("jstr: unescaped control character")
	ErrEscape       = errors.New("jstr: invalid escape sequence")
	ErrMissingQuote = errors.New("jstr: missing closing quote")
	ErrTrailing     = errors.New("jstr: trailing bytes after closing quote")
	ErrInvalidUTF8  = errors.New("jstr: invalid UTF-8")
)

// DecodeError 携带错误类别（可 errors.Is 到上面的哨兵）与 0 基字节偏移。
type DecodeError struct {
	Kind   error
	Offset int
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("%s at byte %d", e.Kind, e.Offset)
}

func (e *DecodeError) Is(target error) bool { return target == e.Kind }

func (e *DecodeError) Unwrap() error { return e.Kind }

// Decode 严格解码一个含两端引号的 JSON 字符串字面量。
func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	return d.Close()
}

// Decoder 是流式严格解码器：按任意切分喂入字节，结果与错误都与整段喂入一致。
type Decoder struct {
	out  []byte
	pos  int // 已接收字节数，也是下一字节的 0 基偏移
	step uint8

	// 多字节未转义 UTF-8 序列的累积状态。
	need     int
	seqLen   int
	acc      rune
	seqStart int

	// \uXXXX 与代理对状态。
	escAt       int
	hexN        int
	hex         [4]byte
	uAt         int
	pairPending bool

	err    error
	closed bool

	// checks 记录字节被检查的总次数；状态机单次推进，故每字节恰好 1 次。
	checks int
}

const (
	stStart = iota
	stRaw
	stEsc
	stU
	stPair
	stDone
)

// NewDecoder 返回一个空的流式解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Checks 返回字节被检查的总次数（非导出计数器的只读视图）。
func (d *Decoder) Checks() int { return d.checks }

// Result 返回已解码的字符串（Close 成功后调用）。
func (d *Decoder) Result() string { return string(d.out) }

func (d *Decoder) fail(kind error, offset int) error {
	d.err = &DecodeError{Kind: kind, Offset: offset}
	return d.err
}

func (d *Decoder) failEsc(r esc.Reason, offset int) error {
	d.err = &DecodeError{Kind: ErrEscape, Offset: offset}
	return fmt.Errorf("%w: %w", d.err, &esc.ErrEscape{Reason: r, Offset: offset})
}

// Write 喂入字面量的下一段字节；错误是黏性的，之后的 Write 不再生效。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for _, b := range p {
		d.checks++
		at := d.pos
		d.pos++
		switch d.step {
		case stStart:
			if b != '"' {
				return 0, d.fail(ErrMissingQuote, 0)
			}
			d.step = stRaw
		case stDone:
			return 0, d.fail(ErrTrailing, at)
		case stRaw:
			if err := d.rawByte(b, at); err != nil {
				return 0, err
			}
		case stEsc:
			if err := d.escByte(b, at); err != nil {
				return 0, err
			}
		case stU:
			if err := d.hexByte(b); err != nil {
				return 0, err
			}
		case stPair:
			if err := d.pairByte(b); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}

// pairByte 在高代理后强制要求下一个单元以 \uXXXX 低代理形式出现。
func (d *Decoder) pairByte(b byte) error {
	switch {
	case b == '"': // 字面量在此结束 → 高代理孤立
		return d.failEsc(esc.RLoneHigh, d.uAt)
	case b == '\\':
		d.step, d.escAt = stEsc, d.pos-1
	default:
		return d.failEsc(esc.RBadLow, d.pos-1)
	}
	return nil
}

func (d *Decoder) rawByte(b byte, at int) error {
	if d.need > 0 {
		if b < 0x80 || b > 0xBF {
			return d.fail(ErrInvalidUTF8, d.seqStart)
		}
		d.acc = d.acc<<6 | rune(b&0x3F)
		d.need--
		if d.need == 0 {
			if err := d.emitRaw(at); err != nil {
				return err
			}
		}
		return nil
	}
	switch {
	case b == '"':
		d.step = stDone
	case b == '\\':
		d.step = stEsc
		d.escAt = at
	case b < 0x20:
		return d.fail(ErrControl, at)
	case b < 0x80:
		d.out = append(d.out, b)
	case b < 0xC2 || b >= 0xF5: // 续行字节、C0/C1 超长引导、>F4 越界
		return d.fail(ErrInvalidUTF8, at)
	case b >= 0xF0:
		d.startSeq(3, b&0x07, at)
	case b >= 0xE0:
		d.startSeq(2, b&0x0F, at)
	default: // 0xC2..0xDF
		d.startSeq(1, b&0x1F, at)
	}
	return nil
}

func (d *Decoder) startSeq(need int, lead byte, at int) {
	d.need, d.seqLen, d.acc, d.seqStart = need, need+1, rune(lead), at
}

// emitRaw 在一个多字节序列收齐后校验超长、代理区与越界。
func (d *Decoder) emitRaw(at int) error {
	r := d.acc
	if r < 0x80 ||
		(d.seqLen == 3 && r < 0x800) ||
		(d.seqLen == 4 && r < 0x10000) ||
		(r >= 0xD800 && r <= 0xDFFF) ||
		r > 0x10FFFF {
		return d.fail(ErrInvalidUTF8, d.seqStart)
	}
	d.out = appendRune(d.out, r)
	_ = at
	return nil
}

func (d *Decoder) escByte(b byte, at int) error {
	if b == 'u' {
		d.step, d.hexN, d.uAt = stU, 0, d.escAt
		return nil
	}
	r, ok := esc.Simple(b)
	if !ok {
		if d.pairPending {
			return d.failEsc(esc.RBadLow, d.escAt)
		}
		return d.failEsc(esc.RUnknown, d.escAt)
	}
	if d.pairPending { // 高代理后必须紧跟 \uXXXX，短转义不算低代理
		return d.failEsc(esc.RBadLow, d.escAt)
	}
	d.out = appendRune(d.out, r)
	d.step = stRaw
	_ = at
	return nil
}

func (d *Decoder) hexByte(b byte) error {
	v, ok := esc.HexDigit(b)
	if !ok {
		return d.failEsc(esc.RShortHex, d.uAt)
	}
	d.hex[d.hexN] = b
	d.hexN++
	if d.hexN < 4 {
		return nil
	}
	r, _ := esc.Hex4(d.hex[:])
	d.step = stRaw
	if d.pairPending {
		d.pairPending = false
		if !esc.IsLow(r) {
			return d.failEsc(esc.RBadLow, d.uAt)
		}
		cp, _ := esc.Pair(d.acc, r)
		d.out = appendRune(d.out, cp)
		return nil
	}
	switch {
	case esc.IsHigh(r):
		d.step, d.acc, d.pairPending = stPair, r, true
	case esc.IsLow(r):
		return d.failEsc(esc.RLoneLow, d.uAt)
	default:
		d.out = appendRune(d.out, r)
	}
	return nil
}

// Close 表示字面量喂入完毕；返回解码结果或带偏移的错误。
func (d *Decoder) Close() (string, error) {
	if d.err != nil {
		return "", d.err
	}
	if d.closed {
		return d.Result(), nil
	}
	d.closed = true
	switch d.step {
	case stDone:
		return d.Result(), nil
	case stStart:
		return "", d.fail(ErrMissingQuote, 0)
	case stU:
		return "", d.failEsc(esc.RShortHex, d.uAt)
	case stPair:
		return "", d.failEsc(esc.RLoneHigh, d.uAt)
	case stEsc:
		if d.pairPending {
			return "", d.failEsc(esc.RLoneHigh, d.uAt)
		}
		return "", d.fail(ErrMissingQuote, d.pos)
	case stRaw:
		if d.need > 0 {
			return "", d.fail(ErrInvalidUTF8, d.seqStart)
		}
		return "", d.fail(ErrMissingQuote, d.pos)
	}
	return "", d.fail(ErrMissingQuote, d.pos)
}

func appendRune(buf []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(buf, byte(r))
	case r < 0x800:
		return append(buf, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(buf, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(buf, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
