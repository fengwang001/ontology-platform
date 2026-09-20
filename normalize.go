package ontology

import (
	"strings"
	"unicode"
)

// Normalizer 决定键在比较前如何规范化。
// 规范化只用于比较，绝不改变存储与返回的值。
type Normalizer struct {
	// TrimSpace 裁剪前后空白（Unicode 空白）。
	TrimSpace bool
	// CaseFold 使用 Unicode 大小写折叠（非简单 ASCII 转换）。
	CaseFold bool
}

// Normalize 返回 s 的规范化形式，仅用于比较。
func (n Normalizer) Normalize(s string) string {
	if n.TrimSpace {
		s = strings.TrimSpace(s)
	}
	if n.CaseFold {
		s = caseFold(s)
	}
	return s
}

// foldExpand 是大小写折叠中的多字符展开（Full Case Folding），
// 无法用单 rune 映射表达，必须先展开再逐 rune 折叠。
var foldExpand = map[rune]string{
	'ß': "ss",
	'ẞ': "ss",
	'İ': "i̇",
}

// caseFold 实现 Unicode 大小写折叠：先做全折叠展开，
// 再把每个 rune 映射到其 SimpleFold 等价类的代表元，
// 使同一等价类（如 Σ/σ/ς、K/k/K）折叠结果一致。
func caseFold(s string) string {
	var exp strings.Builder
	exp.Grow(len(s))
	for _, r := range s {
		if e, ok := foldExpand[r]; ok {
			exp.WriteString(e)
		} else {
			exp.WriteRune(r)
		}
	}
	var b strings.Builder
	b.Grow(exp.Len())
	for _, r := range exp.String() {
		b.WriteRune(foldRepresentative(r))
	}
	return b.String()
}

// foldRepresentative 返回 r 所在 SimpleFold 等价类的最小 rune，
// 作为该等价类的规范代表。等价类内所有 rune 映射到同一代表，
// 因此折叠结果只用于比较即可保证一致性。
func foldRepresentative(r rune) rune {
	rep := r
	for c := unicode.SimpleFold(r); c != r; c = unicode.SimpleFold(c) {
		if c < rep {
			rep = c
		}
	}
	return rep
}
