// Package rle 实现带转义的文本游程编解码，并提供严格的流式解码器。
package rle

import (
	"bytes"
	"errors"
	"math/big"
	"ontology/runs"
	"unicode/utf8"
)

var (
	ErrCountOne         = errors.New("rle: explicit count of 1 is non-canonical")
	ErrCountZero        = errors.New("rle: count must be positive")
	ErrCountLeadingZero = errors.New("rle: count has leading zero")
	ErrAdjacentSame     = errors.New("rle: adjacent runs with same symbol")
	ErrBadEscape        = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash    = errors.New("rle: dangling backslash at end of input")
	ErrMissingSymbol    = errors.New("rle: count without following symbol")
	ErrInvalidUTF8      = errors.New("rle: invalid UTF-8 in symbol")
	ErrLimitExceeded    = errors.New("rle: decoded output exceeds configured limit")
)

// DecodeError 携带哨兵原因与 0 基字节偏移。
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }
func fail(off int, err error) error  { return &DecodeError{Err: err, Offset: off} }

// DefaultLimit 是解码输出字节数的默认上限（64 MiB）。
const DefaultLimit = 64 << 20

func appendEscaped(b []byte, r rune) []byte {
	if r == '\\' || r >= '0' && r <= '9' {
		b = append(b, '\\')
	}
	var buf [utf8.UTFMax]byte
	return append(b, buf[:utf8.EncodeRune(buf[:], r)]...)
}

// Encode 返回 s 的规范游程编码；空串编码为空串。
func Encode(s string) string {
	var b []byte
	for _, run := range runs.Split(s) {
		if run.Count.Cmp(big.NewInt(1)) > 0 {
			b = runs.AppendCount(b, run.Count)
		}
		b = appendEscaped(b, run.Symbol)
	}
	return string(b)
}

// Decode 严格解码 t；任何不满足 Encode(Decode(t)) == t 的输入都返回 *DecodeError。
func Decode(t string) (string, error) {
	var buf bytes.Buffer
	d := NewDecoder(&buf)
	if _, err := d.Write([]byte(t)); err != nil {
		return "", err
	}
	return buf.String(), d.Close()
}

const (
	modeNormal = iota
	modeCount
	modeEsc
	modeMB
)

// Decoder 是流式严格解码器：可按任意字节边界喂入，结论与一次性解码一致。
type Decoder struct {
	out          *bytes.Buffer
	limit, wrote int64
	mode         int
	count        []byte
	countOff     int
	runOff       int
	prev         rune
	prevRaw      []byte
	prevCount    *big.Int
	pending      bool
	sym          []byte
	symOff       int
	closed       bool
	bytesChecked int
}

// NewDecoder 使用默认输出上限构造解码器。
func NewDecoder(out *bytes.Buffer) *Decoder { return NewDecoderLimit(out, 0) }

// NewDecoderLimit 使用给定输出字节上限构造解码器；limit<=0 时使用 DefaultLimit。
func NewDecoderLimit(out *bytes.Buffer, limit int64) *Decoder {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Decoder{out: out, limit: limit, prevCount: big.NewInt(1)}
}

// BytesChecked 返回输入字节被检查的总次数。
func (d *Decoder) BytesChecked() int { return d.bytesChecked }

// Write 喂入一段编码字节。
func (d *Decoder) Write(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		d.bytesChecked++
		if err := d.byte(p[i]); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

func (d *Decoder) byte(c byte) error {
	switch d.mode {
	case modeCount:
		if isDigit(c) {
			d.count = append(d.count, c)
			return nil
		}
		return d.startSym(c)
	case modeEsc:
		d.mode = modeNormal
		if c == '\\' || isDigit(c) {
			return d.finish(rune(c), []byte{c})
		}
		return fail(d.symOff, ErrBadEscape)
	case modeMB:
		d.sym = append(d.sym, c)
		if !utf8.FullRune(d.sym) {
			if c&0xC0 != 0x80 {
				return fail(d.symOff, ErrInvalidUTF8)
			}
			return nil
		}
		r, _ := utf8.DecodeRune(d.sym)
		if r == utf8.RuneError {
			return fail(d.symOff, ErrInvalidUTF8)
		}
		return d.finish(r, append([]byte(nil), d.sym...))
	default:
		if isDigit(c) {
			d.mode, d.countOff, d.runOff, d.count = modeCount, d.bytesChecked-1, d.bytesChecked-1, []byte{c}
			return nil
		}
		return d.startSym(c)
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (d *Decoder) startSym(c byte) error {
	d.runOff = d.bytesChecked - 1
	switch {
	case c == '\\':
		d.mode, d.symOff = modeEsc, d.runOff
	case c < 0x80:
		d.mode = modeNormal
		return d.finish(rune(c), []byte{c})
	case c < 0xC2:
		return fail(d.runOff, ErrInvalidUTF8)
	default:
		d.mode, d.symOff, d.sym = modeMB, d.runOff, []byte{c}
	}
	return nil
}

func (d *Decoder) finish(r rune, raw []byte) error {
	d.mode = modeNormal
	count, off, err := d.parseCount()
	if err != nil {
		return err
	}
	if d.pending && d.prev == r {
		return fail(off, ErrAdjacentSame)
	}
	if d.pending {
		if err := d.writeRun(d.prevRaw, d.prevCount); err != nil {
			return fail(off, err)
		}
	}
	d.prev, d.prevRaw, d.prevCount, d.pending = r, raw, count, true
	return nil
}

func (d *Decoder) parseCount() (*big.Int, int, error) {
	off := d.runOff
	if len(d.count) == 0 {
		return big.NewInt(1), off, nil
	}
	switch {
	case len(d.count) > 1 && d.count[0] == '0':
		return nil, d.countOff, fail(d.countOff, ErrCountLeadingZero)
	case string(d.count) == "0":
		return nil, d.countOff, fail(d.countOff, ErrCountZero)
	}
	n, _, _ := runs.ParseCount(d.count)
	d.count = d.count[:0]
	if n.Cmp(big.NewInt(1)) == 0 {
		return nil, d.countOff, fail(d.countOff, ErrCountOne)
	}
	return n, off, nil
}

func (d *Decoder) writeRun(raw []byte, count *big.Int) error {
	need := new(big.Int).Mul(count, big.NewInt(int64(len(raw))))
	if new(big.Int).Add(big.NewInt(d.wrote), need).Cmp(big.NewInt(d.limit)) > 0 {
		return ErrLimitExceeded
	}
	remaining := new(big.Int).Set(count)
	step := big.NewInt(int64(4096 / len(raw)))
	if step.Sign() == 0 {
		step = big.NewInt(1)
	}
	for remaining.Sign() > 0 {
		k := new(big.Int).Set(step)
		if k.Cmp(remaining) > 0 {
			k.Set(remaining)
		}
		d.out.Write(bytes.Repeat(raw, int(k.Int64())))
		remaining.Sub(remaining, k)
	}
	d.wrote += need.Int64()
	return nil
}

// Close 标记输入结束并冲刷最后一个游程。
func (d *Decoder) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	switch d.mode {
	case modeEsc:
		return fail(d.symOff, ErrTrailingSlash)
	case modeCount:
		return fail(d.countOff, ErrMissingSymbol)
	case modeMB:
		return fail(d.symOff, ErrInvalidUTF8)
	}
	if d.pending {
		if err := d.writeRun(d.prevRaw, d.prevCount); err != nil {
			return fail(d.runOff, err)
		}
		d.pending = false
	}
	return nil
}
