package qp

import "ontology/qpline"

// Encoder 实现 RFC 2045 Quoted-Printable 编码。
type Encoder struct {
	// checks 记录输入字节在编码判定中被检查的总次数。
	// 每个输入字节至多检查 2 次（宽度判定与 token 写出各一次），
	// 插软换行时只重置列计数，不回退重扫，故 checks <= 2*len(input)。
	checks int64
}

// NewEncoder 创建编码器。
func NewEncoder() *Encoder { return &Encoder{} }

// Checks 返回截至目前输入字节被检查的总次数（非导出计数器的只读视图）。
func (e *Encoder) Checks() int64 { return e.checks }

// Encode 编码整段输入。换行 \n 与 \r\n 统一输出为 \r\n；单独的 \r 按
// 普通字节转义为 =0D。每行（不含行尾 \r\n）至多 76 字符，超出部分
// 用软换行 =\\r\\n 分隔，且 =XX 绝不会被拆开。
func (e *Encoder) Encode(src []byte) []byte {
	dst := make([]byte, 0, len(src))
	col := 0
	for i := 0; i < len(src); {
		if src[i] == '\n' {
			dst = append(dst, '\r', '\n')
			col = 0
			i++
			continue
		}
		if i+1 < len(src) && src[i] == '\r' && src[i+1] == '\n' {
			dst = append(dst, '\r', '\n')
			col = 0
			i += 2
			continue
		}
		b := src[i]
		j := i + 1
		// 本行在该 token 之后的第一个字节：换行符或输入结束表示末 token。
		last := j == len(src) || src[j] == '\n' ||
			(src[j] == '\r' && j+1 < len(src) && src[j+1] == '\n')
		e.checks++
		w := qpline.EncodedWidth(b, last)
		if !qpline.Fits(col, w, last) {
			dst = append(dst, qpline.SoftBreak()...)
			col = 0
		}
		e.checks++
		dst = qpline.AppendToken(dst, b, last)
		col += w
		i++
}
	return dst
}

// Encode 是无状态便捷编码。
func Encode(src []byte) []byte { return NewEncoder().Encode(src) }
