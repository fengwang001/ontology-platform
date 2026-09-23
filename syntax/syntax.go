// Package syntax 把通配模式编译成段序列与原子序列。
//
// 段由 '/' 分隔；恰为 "**" 的段标记为 DoubleStar，其余段由原子组成。
// 原子：Lit（逐字节字面量）、Star（*）、Any（?）、Chr（[...]）。
package syntax

import (
	"errors"
	"strings"

	"ontology/class"
	"ontology/runes"
)

// AtomKind 标识原子种类。
type AtomKind int

const (
	KindLit AtomKind = iota
	KindStar
	KindAny
	KindChr
)

// Atom 是段内的最小匹配单位。
type Atom struct {
	Kind AtomKind
	Raw  string       // KindLit：原始字节；KindChr：类原文（用于 Explain 无关的展示）
	Cls  *class.Class // KindChr
}

// Seg 是一个 '/'-分隔段。
type Seg struct {
	DoubleStar bool // 整段恰为 "**"
	Atoms      []Atom
}

// Pattern 是编译结果。
type Pattern struct {
	Raw   string
	Segs  []Seg
	atoms int
}

// Atoms 返回全模式原子总数（每个 ** 段计 1）。
func (p *Pattern) Atoms() int { return p.atoms }

// Limits 为可选的资源上限；零值字段采用默认值。
type Limits struct {
	MaxBytes      int // 模式最大字节数
	MaxDoubleStar int // 单模式最大 ** 段数
}

// 默认上限。
const (
	DefaultMaxBytes      = 4096
	DefaultMaxDoubleStar = 32
)

var (
	// ErrTrailingEscape：模式以单个反斜杠结尾。
	ErrTrailingEscape = errors.New("syntax: trailing backslash")
	// ErrTooLong：模式字节数超限。
	ErrTooLong = errors.New("syntax: pattern too long")
	// ErrTooManyDoubleStar：** 段数超限。
	ErrTooManyDoubleStar = errors.New("syntax: too many '**' segments")
)

// OffsetError 为带模式字节偏移的错误包装。
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Compile 编译模式；lim 为 nil 时采用默认上限。
func Compile(pat string, lim *Limits) (*Pattern, error) {
	maxBytes, maxDS := 0, 0
	if lim != nil {
		maxBytes = lim.MaxBytes
		maxDS = lim.MaxDoubleStar
	}
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxDS == 0 {
		maxDS = DefaultMaxDoubleStar
	}
	if len(pat) > maxBytes {
		return nil, ErrTooLong
	}
	p := &Pattern{Raw: pat}
	ds := 0
	segOff := 0
	for rest := pat; ; {
		i := strings.IndexByte(rest, '/')
		rawSeg := rest
		if i >= 0 {
			rawSeg = rest[:i]
		}
		if rawSeg == "**" {
			ds++
			if ds > maxDS {
				return nil, ErrTooManyDoubleStar
			}
			p.Segs = append(p.Segs, Seg{DoubleStar: true})
			p.atoms++
		} else {
			seg, err := parseSeg(rawSeg, segOff)
			if err != nil {
				return nil, err
			}
			p.Segs = append(p.Segs, seg)
			p.atoms += len(seg.Atoms)
		}
		segOff += len(rawSeg) + 1
		if i < 0 {
			break
		}
		rest = rest[i+1:]
	}
	return p, nil
}

// parseSeg 解析非 ** 段；需要模式全文以给出绝对字节偏移。
func parseSeg(raw string, off int) (Seg, error) {
	seg := Seg{}
	i := 0
	for i < len(raw) {
		abs := off + i
		switch raw[i] {
		case '*':
			seg.Atoms = append(seg.Atoms, Atom{Kind: KindStar})
			i++
		case '?':
			seg.Atoms = append(seg.Atoms, Atom{Kind: KindAny})
			i++
		case '[':
			cls, next, err := class.Parse(raw, i)
			if err != nil {
				var ce *class.Error
				if errors.As(err, &ce) {
					return Seg{}, &OffsetError{Offset: off + ce.Offset, Err: ce}
				}
				return Seg{}, err
			}
			seg.Atoms = append(seg.Atoms, Atom{Kind: KindChr, Raw: raw[i:next], Cls: cls})
			i = next
		case '\\':
			if i+1 >= len(raw) {
				return Seg{}, &OffsetError{Offset: abs, Err: ErrTrailingEscape}
			}
			u := runes.DecodeAt(raw, i+1)
			seg.Atoms = append(seg.Atoms, Atom{Kind: KindLit, Raw: u.Raw})
			i += 1 + len(u.Raw)
		default:
			u := runes.DecodeAt(raw, i)
			seg.Atoms = append(seg.Atoms, Atom{Kind: KindLit, Raw: u.Raw})
			i += len(u.Raw)
		}
	}
	return seg, nil
}
