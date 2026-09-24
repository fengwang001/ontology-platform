// Package rle 在 runs 包之上实现带转义的文本游程编码编解码器。
package rle

import (
	"errors"
	"strconv"
	"strings"

	"ontology/runs"
)

// 八类彼此可区分的严格性错误，外加输出上限错误。
var (
	ErrCountOne      = errors.New("explicit count of 1")
	ErrCountZero     = errors.New("count of 0")
	ErrLeadingZero   = errors.New("leading zero in count")
	ErrAdjacent      = errors.New("adjacent runs with same symbol")
	ErrBadEscape     = errors.New("backslash not followed by digit or backslash")
	ErrTrailingSlash = errors.New("dangling trailing backslash")
	ErrTrailingCount = errors.New("trailing count without symbol")
	ErrInvalidUTF8   = errors.New("invalid UTF-8 in symbol")
	ErrOutputLimit   = errors.New("decoded output exceeds limit")
)

// DecodeError 携带可判定的哨兵原因与违规起始字节偏移。
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string {
	return "rle: " + e.Err.Error() + " at offset " + strconv.Itoa(e.Offset)
}
func (e *DecodeError) Unwrap() error { return e.Err }

type Option func(*Decoder)

// WithMaxOut 限制解码输出的总字节数；n<=0 表示不限。
func WithMaxOut(n int64) Option { return func(d *Decoder) { d.maxOut = n } }

// NewDecoder 创建流式解码器，默认输出上限 64 MiB。
func NewDecoder(opts ...Option) *Decoder {
	d := &Decoder{maxOut: 64 << 20}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Decode 一次性严格解码。
func Decode(t string) (string, error) {
	d := NewDecoder()
	if _, err := d.WriteString(t); err != nil || d.Close() != nil {
		return "", d.err
	}
	return d.String(), nil
}

func (d *Decoder) WriteString(t string) (int, error) { return d.Write([]byte(t)) }
func (d *Decoder) String() string                    { return d.out.String() }

func (d *Decoder) fail(e error, off int) error {
	if d.err == nil {
		d.err = &DecodeError{Err: e, Offset: off}
	}
	return d.err
}

// Encode 返回 s 的规范游程编码：最长游程、次数 1 省略、数字与反斜杠转义。
func Encode(s string) string {
	var b strings.Builder
	runs.Split(s, func(run runs.Run) bool {
		if run.N.CmpUint64(1) != 0 {
			b.WriteString(run.N.String())
		}
		if r := run.Rune; r == '\\' || r >= '0' && r <= '9' {
			b.WriteByte('\\')
		}
		b.WriteRune(run.Rune)
		return true
	})
	return b.String()
}
