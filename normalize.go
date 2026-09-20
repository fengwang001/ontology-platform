package ontology

import (
	"strings"
	"unicode"
)

// NormOptions 控制键规范化的两条独立规则。
// 规范化只用于冲突比较，绝不改变存储与返回的值。
type NormOptions struct {
	// TrimSpace 裁剪前后 Unicode 空白。
	TrimSpace bool
	// CaseFold 使用 Unicode 大小写折叠（非简单 ASCII 转换 tolower）。
	CaseFold bool
}

// Normalize 按选项对字符串做规范化，返回比较用键。
func (o NormOptions) Normalize(s string) string {
	if o.TrimSpace {
		s = strings.TrimSpace(s)
	}
	if o.CaseFold {
		s = caseFold(s)
	}
	return s
}

// caseFold 做 Unicode 全大小写折叠（full case folding）：
// 先查多字符折叠表（如 ß→ss、İ→i̇），否则用 unicode.ToLower。
// 它能正确处理非 ASCII 字符，例如希腊语词尾 ς 与 σ 折叠后相同。
func caseFold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if rep, ok := fullFold[r]; ok {
			for _, rr := range rep {
				b.WriteRune(foldRune(rr))
			}
			continue
		}
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldRune 返回单字符的折叠规范形：在其 SimpleFold 轨道上，
// 对每个成员取小写后取最小码点。同一轨道的所有字符（如 Σ/σ/ς、
// K/k/U+212A）都归一到同一键，且结果偏向小写形式。
func foldRune(r rune) rune {
	min := unicode.ToLower(r)
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if l := unicode.ToLower(f); l < min {
			min = l
		}
	}
	return min
}
