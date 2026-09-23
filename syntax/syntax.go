// Package syntax 把通配模式编译为段序列与原子序列，并给出可判定、带字节偏移的语法错误。
package syntax

import (
	"errors"

	"ontology/class"
	"ontology/runes"
)

// Kind 标识四类彼此可区分的语法错误。
type Kind int

const (
	KindUnterminatedClass Kind = iota // 未闭合的 '['（含类内末尾单个 '\'）
	KindReversedRange                 // 颠倒区间 [z-a]
	KindTrailingSlash                 // 模式以单个 '\' 结尾
	KindEmptyClass                    // 空类 []
)

// Error 携带错误类别与模式中的字节偏移。
type Error struct {
	Kind   Kind
	Offset int
	err    error
}

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return e.err }

// Limits 是编译期资源上限，零值字段表示使用 DefaultLimits 中的对应值。
type Limits struct {
	MaxPatternBytes int
	MaxDoubleStars  int
}

// DefaultLimits 是 Compile 使用的默认上限。
var DefaultLimits = Limits{MaxPatternBytes: 64 * 1024, MaxDoubleStars: 64}

// AtomKind 是段内原子的种类。
type AtomKind int

const (
	AtomLiteral AtomKind = iota
	AtomQuestion
	AtomStar
	AtomClass
)

// Atom 是段内的一个匹配单位。
type Atom struct {
	Kind  AtomKind
	Lit   runes.Rune
	Class *class.Class
}

// Segment 是一个由 '/' 分隔的模式段。
type Segment struct {
	Atoms      []Atom
	DoubleStar bool // 该段恰好是独占的 "**"
}

// Pattern 是编译结果，编译失败时不会返回非 nil 的 Pattern。
type Pattern struct {
	Segments []Segment
	Empty    bool // 模式为空串
	raw      string
	limits   Limits
}

// Raw 返回编译时使用的模式原文。
func (p *Pattern) Raw() string { return p.raw }

func (l Limits) orDefault() Limits {
	d := DefaultLimits
	if l.MaxPatternBytes > 0 {
		d.MaxPatternBytes = l.MaxPatternBytes
	}
	if l.MaxDoubleStars > 0 {
		d.MaxDoubleStars = l.MaxDoubleStars
	}
	return d
}

// Compile 用默认上限编译字符串模式。
func Compile(pattern string) (*Pattern, error) {
	return CompileWithLimits(pattern, Limits{})
}

// CompileWithLimits 使用给定上限编译。
func CompileWithLimits(pattern string, l Limits) (*Pattern, error) {
	return compile([]byte(pattern), l.orDefault())
}

func compile(raw []byte, lim Limits) (*Pattern, error) {
	if len(raw) > lim.MaxPatternBytes {
		return nil, errors.New("syntax: pattern exceeds max bytes")
	}
	p := &Pattern{Empty: len(raw) == 0, raw: string(raw), limits: lim}
	if p.Empty {
		return p, nil
	}
	seg := Segment{}
	starCount := 0
	flushStar := func() {
		if starCount > 0 {
			seg.Atoms = append(seg.Atoms, Atom{Kind: AtomStar})
			starCount = 0
		}
	}
	for i := 0; i < len(raw); {
		b := raw[i]
		switch {
		case b == '/':
			n := starCount
			flushStar()
			// 该段所有字节都是 '*'（flush 前 starCount 即其个数）且恰好 2 个。
			p.Segments = append(p.Segments, makeSeg(seg, n))
			seg = Segment{}
			i++
		case b == '*':
			starCount++
			i++
		case b == '?':
			flushStar()
			seg.Atoms = append(seg.Atoms, Atom{Kind: AtomQuestion})
			i++
		case b == '[':
			flushStar()
			cl, next, err := class.Parse(raw[i:])
			if err != nil {
				return nil, mapClassError(err, i)
			}
			seg.Atoms = append(seg.Atoms, Atom{Kind: AtomClass, Class: cl})
			i += next
		case b == '\\':
			if i+1 >= len(raw) {
				return nil, &Error{Kind: KindTrailingSlash, Offset: i, err: errors.New("syntax: trailing backslash")}
			}
			flushStar()
			r := runes.Decode(raw, i+1)
			seg.Atoms = append(seg.Atoms, Atom{Kind: AtomLiteral, Lit: r})
			i += 1 + r.Width
		default:
			flushStar()
			r := runes.Decode(raw, i)
			seg.Atoms = append(seg.Atoms, Atom{Kind: AtomLiteral, Lit: r})
			i += r.Width
		}
	}
	// 标记独占的 "**" 段：该段仅有星号且恰好两个。
	n := starCount
	flushStar()
	p.Segments = append(p.Segments, makeSeg(seg, n))
	dn := 0
	for _, s := range p.Segments {
		if s.DoubleStar {
			dn++
		}
	}
	if dn > lim.MaxDoubleStars {
		return nil, errors.New("syntax: too many '**' segments")
	}
	return p, nil
}

func makeSeg(seg Segment, stars int) Segment {
	if len(seg.Atoms) == 1 && seg.Atoms[0].Kind == AtomStar && stars == 2 {
		seg.DoubleStar = true
	}
	return seg
}

func mapClassError(err error, off int) error {
	k := KindUnterminatedClass
	switch {
	case errors.Is(err, class.ErrReversed):
		k = KindReversedRange
	case errors.Is(err, class.ErrEmpty):
		k = KindEmptyClass
	}
	return &Error{Kind: k, Offset: off, err: err}
}
