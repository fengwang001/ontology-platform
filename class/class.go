// Package class 解析并匹配字符类 [...]。
// 支持取反（[!...] 与 [^...]）、区间（a-z）、转义（\]）、] 作为首字符。
// 取反类不匹配 '/'。非法字节只能按原字节字面命中，不进区间。
package class

import (
	"errors"
	"fmt"

	"ontology/runes"
)

var (
	ErrUnclosed = errors.New("class: unclosed '['")
	ErrReversed = errors.New("class: reversed range")
	ErrEmpty    = errors.New("class: empty class")
)

// Error 是带模式中字节偏移的解析错误，Err 为上面三个哨兵之一。
type Error struct {
	Err    error
	Offset int
}

func (e *Error) Error() string { return fmt.Sprintf("%s at byte %d", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

type rng struct{ lo, hi rune }

// Class 是编译好的字符类。
type Class struct {
	negated bool
	ranges  []rng
	raws    []byte // 非法字节字面量
}

// Match 判断码点 t 是否命中该类。
func (c Class) Match(t runes.Token) bool {
	m := c.member(t)
	if c.negated {
		return !m && t.Rune != '/'
	}
	return m
}

func (c Class) member(t runes.Token) bool {
	if t.Rune == runes.RuneError && t.Size == 1 && t.Raw >= 0x80 {
		for _, b := range c.raws {
			if b == t.Raw {
				return true
			}
		}
		return false
	}
	for _, r := range c.ranges {
		if r.lo <= t.Rune && t.Rune <= r.hi {
			return true
		}
	}
	return false
}

// Parse 解析 pat 中 start 处（必须是 '['）的字符类，返回类与 ']' 之后的下标。
func Parse(pat []byte, start int) (Class, int, error) {
	var c Class
	i := start + 1
	if i < len(pat) && (pat[i] == '!' || pat[i] == '^') {
		c.negated = true
		i++
	}
	first := true
	for {
		if i >= len(pat) {
			return c, 0, &Error{ErrUnclosed, start}
		}
		if pat[i] == ']' && !first {
			return c, i + 1, nil
		}
		if pat[i] == ']' && first && i+1 == len(pat) {
			return c, 0, &Error{ErrEmpty, start}
		}
		first = false
		lo, next, err := item(pat, i, start)
		if err != nil {
			return c, 0, err
		}
		i = next
		if i < len(pat) && pat[i] == '-' && i+1 < len(pat) && pat[i+1] != ']' {
			hi, next2, err := item(pat, i+1, start)
			if err != nil {
				return c, 0, err
			}
			if hi.Rune < lo.Rune {
				return c, 0, &Error{ErrReversed, i - 1}
			}
			c.add(lo, hi)
			i = next2
		} else {
			c.add(lo, lo)
		}
	}
}

// item 解析类内一个字符（可能是转义），返回其码点与下一字节下标。
func item(pat []byte, i, start int) (runes.Token, int, error) {
	if pat[i] == '\\' {
		if i+1 >= len(pat) {
			return runes.Token{}, 0, &Error{ErrUnclosed, start}
		}
		t := runes.Decode(pat, i+1)
		return t, i + 1 + t.Size, nil
	}
	t := runes.Decode(pat, i)
	return t, i + t.Size, nil
}

func (c *Class) add(lo, hi runes.Token) {
	if lo.Rune == runes.RuneError && lo.Size == 1 && lo.Raw >= 0x80 {
		c.raws = append(c.raws, lo.Raw)
		return
	}
	c.ranges = append(c.ranges, rng{lo.Rune, hi.Rune})
}
