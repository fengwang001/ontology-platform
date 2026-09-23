// Package class 解析 glob 字符类 [...] 并判定单个码点是否属于该类。
package class

import (
	"errors"

	"ontology/runes"
)

// 解析错误，syntax 层会包装并附加模式字节偏移。
var (
	ErrUnterminated = errors.New("class: unterminated '['")
	ErrReversed     = errors.New("class: reversed range [z-a]")
	ErrEmpty        = errors.New("class: empty class []")
)

// Class 是编译后的字符类。
type Class struct {
	negate bool
	ranges []span // 合法码点的闭区间
	bad    []byte // 非法字节成员（原字节）
}

type span struct{ lo, hi rune }

// Parse 解析含外层方括号的字符类（如 "[a-z!]")，返回类与右括号后的字节偏移。
func Parse(p []byte) (*Class, int, error) {
	// 调用方保证 p[0]=='['。
	i := 1
	c := &Class{}
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		c.negate = true
		i++
	}
	// ']' 作为首字符（取反符号之后也算首字符位置）是字面成员；
	// 但恰好 "[]" / "[!]" 视为空类（语法错误）。
	if i < len(p) && p[i] == ']' {
		if i+1 == len(p) {
			return nil, 0, ErrEmpty
		}
		c.add(runes.Rune{R: ']'})
		i++
	}
	for {
		if i >= len(p) {
			return nil, 0, ErrUnterminated
		}
		if p[i] == ']' {
			break
		}
		m, w, err := readMember(p, i)
		if err != nil {
			return nil, 0, err
		}
		i += w
		if i < len(p) && p[i] == '-' && i+1 < len(p) && p[i+1] != ']' {
			i++
			m2, w2, e2 := readMember(p, i)
			if e2 != nil {
				return nil, 0, e2
			}
			if err := c.addRange(m, m2); err != nil {
				return nil, 0, err
			}
			i += w2
			continue
		}
		c.add(m)
	}
	return c, i + 1, nil
}

func readMember(p []byte, i int) (runes.Rune, int, error) {
	if p[i] == '\\' {
		if i+1 >= len(p) {
			return runes.Rune{}, 0, ErrUnterminated
		}
		r := runes.Decode(p, i+1)
		return r, 1 + r.Width, nil
	}
	r := runes.Decode(p, i)
	return r, r.Width, nil
}

func (c *Class) add(r runes.Rune) {
	if r.Bad {
		for _, b := range c.bad {
			if b == r.Raw {
				return
			}
		}
		c.bad = append(c.bad, r.Raw)
		return
	}
	c.ranges = append(c.ranges, span{r.R, r.R})
}

func (c *Class) addRange(a, b runes.Rune) error {
	switch {
	case a.Bad || b.Bad:
		// 非法字节不参与码点区间，按单字节字面成员处理。
		c.add(a)
		c.add(b)
	case a.R > b.R:
		return ErrReversed
	default:
		c.ranges = append(c.ranges, span{a.R, b.R})
	}
	return nil
}

// Match 判定该类是否匹配码点单位 r。取反类额外不匹配 '/'。
func (c *Class) Match(r runes.Rune) bool {
	hit := c.hit(r)
	if c.negate {
		if !r.Bad && r.R == '/' {
			return false
		}
		return !hit
	}
	return hit
}

func (c *Class) hit(r runes.Rune) bool {
	if r.Bad {
		for _, b := range c.bad {
			if b == r.Raw {
				return true
			}
		}
		return false
	}
	for _, s := range c.ranges {
		if r.R < s.lo {
			return false
		}
		if r.R <= s.hi {
			return true
		}
	}
	return false
}
