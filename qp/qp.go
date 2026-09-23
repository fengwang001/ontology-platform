// Package qp 实现 RFC 2045 的 Quoted-Printable 编码器与严格解码器。
// 编码由 qpline 提供单行判定原语，本包负责硬换行、76 字符上限与错误编排。
package qp

import (
	"errors"

	"ontology/qpline"
)

// 五类彼此可区分的哨兵错误；用 errors.Is 判定，具体出错偏移见 DecodeError。
var (
	ErrBadEscape     = errors.New("qp: '=' not followed by two hex digits")
	ErrUnexpectedEOF = errors.New("qp: dangling '=' at end of input")
	ErrLineTooLong   = errors.New("qp: encoded line exceeds 76 characters")
	ErrInvalidByte   = errors.New("qp: unescaped byte outside printable ASCII")
	ErrTrailingSpace = errors.New("qp: unescaped trailing whitespace")
	errClosed        = errors.New("qp: decoder already closed")
)

// DecodeError 携带哨兵错误 Err 与出错字节在整体输入流中的绝对偏移 Offset。
type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string { return e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

// inspected 是编码器维护的非导出计数器，记录输入字节被检查的总次数。
// 每次主循环处理一个字节计一次；每个字节最多再前瞻一次，故总计 <= 2*len(src)。

// Encode 返回 src 的 Quoted-Printable 编码。输入中的 '\n' 与 "\r\n" 一律输出
// 为硬换行 "\r\n"；单独的 '\r' 视为普通字节（=0D）。输出每行（不含行尾）至多 76
// 个字符，软换行 "=\r\n" 的 '=' 计入该上限，且绝不会拆开一个 =XX。
func Encode(src []byte) []byte {
	var inspected int
	out := make([]byte, 0, len(src)+len(src)/4)
	col := 0
	emitSoft := func() { out = append(out, '=', '\r', '\n'); col = 0 }
	for i := 0; i < len(src); {
		inspected++
		if src[i] == '\n' {
			out = append(out, '\r', '\n')
			col = 0
			i++
			continue
		}
		if src[i] == '\r' && i+1 < len(src) {
			inspected++
			if src[i+1] == '\n' {
				out = append(out, '\r', '\n')
				col = 0
				i += 2
				continue
			}
		}
		b := src[i]
		escape := qpline.MustEscape(b)
		if qpline.IsHorizontalSpace(b) && qpline.TrailingSpace(src, i) {
			escape = true
		}
		tokenLen := 1
		if escape {
			tokenLen = 3
		}
		if qpline.BreakBefore(col, tokenLen, i+1 >= len(src)) {
			emitSoft()
		}
		if escape {
			out = append(out, qpline.HexCode(b)...)
		} else {
			out = append(out, b)
		}
		col += tokenLen
		i++
	}
	inspectedCount = inspected
	return out
}

// inspectedCount 保存最近一次 Encode 的检查次数，供同包测试断言复杂度。
var inspectedCount int

// Decode 一次性严格解码 src（内部使用流式 Decoder，切分方式不影响结果）。
func Decode(src []byte) ([]byte, error) {
	d := NewDecoder()
	if _, err := d.Write(src); err != nil {
		return d.Output(), err
	}
	if err := d.Close(); err != nil {
		return d.Output(), err
	}
	return d.Output(), nil
}
