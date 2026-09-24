// Package fmtf 把 dec 生成的最短有效数字组装成最终文本。
package fmtf

import (
	"errors"
	"fmt"
	"strings"

	"ontology/bits"
	"ontology/dec"
)

// ErrNonFinite 表示 NaN 或 Inf 没有可定义的文本形式。
var ErrNonFinite = errors.New("fmtf: NaN 或 Inf 没有文本形式")

// 指数形式选择规则：最高位十进制指数 E 满足 -4 <= E <= 16 用定点形式，
// 否则用指数形式。规则只依赖 E，结果确定且可复现。
const (
	fixedMinExp = -4
	fixedMaxExp = 16
)

// Format 返回 x 的最短往返文本。NaN 与 ±Inf 返回 ErrNonFinite。
func Format(x float64) (string, error) {
	if bits.IsNaN(x) || bits.IsInf(x) {
		return "", ErrNonFinite
	}
	if bits.IsZero(x) {
		if bits.Decompose(x).Neg {
			return "-0", nil
		}
		return "0", nil
	}
	d, err := dec.Shortest(x)
	if err != nil {
		return "", err
	}
	return layout(d), nil
}

func layout(d dec.Decimal) string {
	var b strings.Builder
	if d.Neg {
		b.WriteByte('-')
	}
	n := len(d.Digits)
	if d.Exp < fixedMinExp || d.Exp > fixedMaxExp {
		b.WriteByte(d.Digits[0])
		if n > 1 {
			b.WriteByte('.')
			b.WriteString(d.Digits[1:])
		}
		fmt.Fprintf(&b, "e%+03d", d.Exp)
		return b.String()
	}
	switch {
	case d.Exp >= n-1: // 整数，尾部补零
		b.WriteString(d.Digits)
		b.WriteString(strings.Repeat("0", d.Exp-n+1))
	case d.Exp >= 0: // 小数点在数字串内部
		b.WriteString(d.Digits[:d.Exp+1])
		b.WriteByte('.')
		b.WriteString(d.Digits[d.Exp+1:])
	default: // 0.00…ddd
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -d.Exp-1))
		b.WriteString(d.Digits)
	}
	return b.String()
}
