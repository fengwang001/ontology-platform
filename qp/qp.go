// Package qp 提供 RFC 2045 Quoted-Printable 的编码器与严格流式解码器。
package qp

import "errors"

// Encode 返回 src 的 Quoted-Printable 编码（行尾 CRLF，行长上限 76）。
func Encode(src []byte) []byte { return nil }

// EncodeWithChecks 与 Encode 相同，额外返回 qpline 对输入字节的检查次数。
func EncodeWithChecks(src []byte) ([]byte, int) { return nil, 0 }

// Decode 一次性严格解码 src。
func Decode(src []byte) ([]byte, error) { return nil, nil }

var (
	ErrInvalidEscape = errors.New("qp: invalid escape sequence")
	ErrEOFAfterEqual = errors.New("qp: dangling '=' at end of input")
	ErrLineTooLong   = errors.New("qp: line exceeds 76 characters")
	ErrInvalidByte   = errors.New("qp: unescaped non-printable byte")
	ErrTrailingSpace = errors.New("qp: unescaped trailing whitespace")
)

// Decoder 是流式严格解码器，可按任意切分写入。
type Decoder struct{}

// NewDecoder 创建解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Write 追加输入并解码；出错时返回带偏移的 *DecodeError。
func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }

// Close 结束输入，检查未完成转义与行末空白。
func (d *Decoder) Close() error { return nil }

// Output 返回截至目前的解码结果副本。
func (d *Decoder) Output() []byte { return nil }

// DecodeError 携带哨兵错误与出错字节在整个输入流中的绝对偏移。
type DecodeError struct {
	Err error
	Off int
}

func (e *DecodeError) Error() string { return "" }
func (e *DecodeError) Unwrap() error { return e.Err }
