// Package class 解析并匹配模式中的字符类 [...]。
//
// 支持取反（[!...]、[^...]）、码点区间（[a-z]、[é-ë]）、转义（[\]]）以及
// 首字符 ] 作为字面量（[]a] 匹配 ] 或 a）。取反类不匹配 '/'。
package class

import (
	"errors"

	"ontology/runes"
)

// Kind 是字符类的可判定错误种类。
type Kind int

const (
	KindUnclosed Kind = iota // 类未闭合
	KindEmpty                // 空类 []
	KindReversed             // 颠倒区间 [z-a]
)

// Error 带字节偏移（相对整个模式，调用方传入 start 即类内偏移换算基准）。
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindUnclosed:
		return "class: unclosed '['"
	case KindEmpty:
		return "class: empty class []"
	default:
		return "class: reversed range"
	}
}

var (
	// ErrUnclosed 可用 errors.Is 判定未闭合。
	ErrUnclosed = errors.New("class: unclosed '['")
	// ErrEmpty 判定空类。
	ErrEmpty = errors.New("class: empty class []")
	// ErrReversed 判定颠倒区间。
	ErrReversed = errors.New("class: reversed range")
)

func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnclosed:
		return e.Kind == KindUnclosed
	case ErrEmpty:
		return e.Kind == KindEmpty
	case ErrReversed:
		return e.Kind == KindReversed
	}
	return false
}

type span struct{ lo, hi rune }

// Class 是编译后的字符类。
type Class struct {
	negated bool
	spans   []span
}

// Match 判定码点 r 是否属于该类。取反类永不匹配 '/'。
func (c *Class) Match(r rune) bool {
	if c.negated && r == '/' {
		return false
	}
	hit := false
	for _, sp := range c.spans {
		if r >= sp.lo && r <= sp.hi {
			hit = true
			break
		}
	}
	return hit != c.negated
}

// Parse 从 pat[start]（要求 pat[start]=='['）解析一个字符类。
// 返回编译后的类、类结束后的下一字节偏移，或带偏移的 *Error。
func Parse(pat string, start int) (*Class, int, error) {
	i := start + 1
	neg := false
	if i < len(pat) && (pat[i] == '!' || pat[i] == '^') {
		neg = true
		i++
	}
	c := &Class{negated: neg}
	first := true // 类内容首位的 ']' 作字面量（[]a]）
	for {
		if i >= len(pat) {
			return nil, 0, &Error{Kind: KindUnclosed, Offset: start}
		}
		if pat[i] == ']' && !first {
			i++
			break
		}
		if pat[i] == ']' && first {
			// 首位 ']'：后再无闭合者则为空类（[]、[!]），否则作字面量。
			if !hasClose(pat, i+1) {
				return nil, 0, &Error{Kind: KindEmpty, Offset: start}
			}
			c.spans = append(c.spans, span{']', ']'})
			i++
			first = false
			continue
		}
		first = false
		var lo rune
		var err error
		lo, i, err = item(pat, i, start)
		if err != nil {
			return nil, 0, err
		}
		if i < len(pat) && pat[i] == '-' && i+1 < len(pat) && pat[i+1] != ']' {
			dash := i
			i++ // 跳过 '-'
			var hi rune
			hi, i, err = item(pat, i, start)
			if err != nil {
				return nil, 0, err
			}
			if lo > hi {
				return nil, 0, &Error{Kind: KindReversed, Offset: dash}
			}
			c.spans = append(c.spans, span{lo, hi})
		} else {
			c.spans = append(c.spans, span{lo, lo})
		}
	}
	return c, i, nil
}

func hasClose(pat string, from int) bool {
	for i := from; i < len(pat); i++ {
		if pat[i] == '\\' {
			i++ // 被转义的 ] 不算闭合者
			continue
		}
		if pat[i] == ']' {
			return true
		}
	}
	return false
}

// item 读取类内的一个码点（处理反斜杠转义），返回码点与下一偏移。
// 末尾裸反斜杠视为类未闭合。
func item(pat string, i int, start int) (rune, int, error) {
	if pat[i] == '\\' {
		i++
		if i >= len(pat) {
			return 0, 0, &Error{Kind: KindUnclosed, Offset: start}
		}
	}
	u := runes.DecodeAt(pat, i)
	return u.R, i + len(u.Raw), nil
}
