// Package syntax 把通配模式编译成段序列与原子序列，给出可判定的语法错误。
package syntax

import (
	"errors"
	"fmt"

	"ontology/class"
	"ontology/runes"
)

var (
	ErrTrailingEscape    = errors.New("syntax: pattern ends with '\\'")
	ErrPatternTooLong    = errors.New("syntax: pattern exceeds max bytes")
	ErrTooManyDoubleStar = errors.New("syntax: too many ** segments")
)

// Error 是带模式中字节偏移的语法错误（转义类）；字符类错误见 class.Error。
type Error struct {
	Err    error
	Offset int
}

func (e *Error) Error() string { return fmt.Sprintf("%s at byte %d", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

// Limits 是可配置的资源上限；零值字段取默认值。
type Limits struct {
	MaxPatternBytes int // 单模式最大字节数
	MaxDoubleStar   int // 单模式最大 ** 段数
	MaxRules        int // 规则集最大规则数（set 使用）
}

// Defaults 返回未设置字段填充默认值后的 Limits。
func (l Limits) Defaults() Limits {
	if l.MaxPatternBytes <= 0 {
		l.MaxPatternBytes = 4096
	}
	if l.MaxDoubleStar <= 0 {
		l.MaxDoubleStar = 16
	}
	if l.MaxRules <= 0 {
		l.MaxRules = 1024
	}
	return l
}

// Kind 是原子种类。
type Kind int

const (
	Lit   Kind = iota // 字面码点
	Any               // ?
	Star              // *（段内）
	Class             // [...]
)

// Atom 是段内一个匹配单元，恰好消费一个码点（Star 除外）。
type Atom struct {
	Kind  Kind
	Lit   runes.Token
	Class class.Class
}

// Segment 是模式的一段；DoubleStar 为真时表示整段 **。
type Segment struct {
	DoubleStar bool
	Atoms      []Atom
}

// Pattern 是编译好的模式。
type Pattern struct {
	Raw      string
	Segments []Segment
	NumAtoms int // 每个 ?/*/字面/类 计 1，每个 ** 段计 1
}

// Compile 编译模式；失败返回可判定错误且不产生可用 Pattern。
func Compile(pat string, lim Limits) (*Pattern, error) {
	lim = lim.Defaults()
	if len(pat) > lim.MaxPatternBytes {
		return nil, ErrPatternTooLong
	}
	p := &Pattern{Raw: pat}
	nDouble := 0
	for _, sa := range split(pat) {
		if sa.seg == "**" {
			nDouble++
			if nDouble > lim.MaxDoubleStar {
				return nil, ErrTooManyDoubleStar
			}
			p.Segments = append(p.Segments, Segment{DoubleStar: true})
			p.NumAtoms++
			continue
		}
		s, err := compileSeg(sa.off, sa.seg)
		if err != nil {
			return nil, err
		}
		p.NumAtoms += len(s.Atoms)
		p.Segments = append(p.Segments, s)
	}
	return p, nil
}

// split 按 '/' 切段，返回每段及其在模式中的字节偏移。
func split(pat string) []segAt {
	var out []segAt
	start := 0
	for i := 0; i <= len(pat); i++ {
		if i == len(pat) || pat[i] == '/' {
			out = append(out, segAt{start, pat[start:i]})
			start = i + 1
		}
	}
	return out
}

type segAt struct {
	off int
	seg string
}

func compileSeg(off int, seg string) (Segment, error) {
	var s Segment
	b := []byte(seg)
	for i := 0; i < len(b); {
		switch b[i] {
		case '?':
			s.Atoms = append(s.Atoms, Atom{Kind: Any})
			i++
		case '*':
			s.Atoms = append(s.Atoms, Atom{Kind: Star})
			i++
		case '[':
			c, next, err := class.Parse(b, i)
			if err != nil {
				if ce, ok := err.(*class.Error); ok {
					ce.Offset += off
				}
				return s, err
			}
			s.Atoms = append(s.Atoms, Atom{Kind: Class, Class: c})
			i = next
		case '\\':
			if i+1 >= len(b) {
				return s, &Error{ErrTrailingEscape, off + i}
			}
			t := runes.Decode(b, i+1)
			s.Atoms = append(s.Atoms, Atom{Kind: Lit, Lit: t})
			i += 1 + t.Size
		default:
			t := runes.Decode(b, i)
			s.Atoms = append(s.Atoms, Atom{Kind: Lit, Lit: t})
			i += t.Size
		}
	}
	return s, nil
}
