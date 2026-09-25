// Package patch 把统一格式补丁应用到文本，支持偏移查找、原子拒绝、反向与并发文档存储。
package patch

import (
	"errors"

	"ontology/lines"
	"ontology/udiff"

	"ontology/edit"
	"ontology/hunk"
)

// ErrTooDifferent 与 edit 距离上限对应，供生成侧判别。
var ErrTooDifferent = edit.ErrTooDifferent

// Category 是失败类别。
type Category int

const (
	CatFormat Category = iota
	CatContext
	CatOffset
	CatTooDifferent
)

// ApplyError 是可判定错误：类别、第几个 hunk（0 起）与说明。
type ApplyError struct {
	Cat    Category
	Hunk   int
	Reason string
}

func (e *ApplyError) Error() string { return "patch: " + e.Reason }

// Options 控制应用行为。
type Options struct {
	Fuzz    int // 记录位置上下搜索行数 F
	MaxSize int // 补丁最大字节数，<=0 不限
	MaxHunk int // hunk 数上限，<=0 不限
}

// DefaultOptions 是 F=3 的默认配置。
var DefaultOptions = Options{Fuzz: 3}

// Diff 生成 a→b 的补丁文本（C=3）。
func Diff(a, b []byte) ([]byte, error) {
	r, err := edit.Diff(lines.Split(a), lines.Split(b), -1)
	if err != nil {
		return nil, err
	}
	return udiff.Render(toPatch(hunk.Build(r, 3))), nil
}

func toPatch(hs []hunk.Hunk) udiff.Patch {
	p := udiff.Patch{}
	for _, hh := range hs {
		u := udiff.Hunk{OldStart: hh.OldStart, OldCount: hh.OldCount,
			NewStart: hh.NewStart, NewCount: hh.NewCount}
		for _, e := range hh.Body {
			u.Body = append(u.Body, udiff.Entry{Kind: e.Kind, Line: e.Line,
				NoNL: !e.Line.Terminated()})
		}
		p.Hunks = append(p.Hunks, u)
	}
	return p
}

// Apply 在 opts 下把补丁 p 应用到 a；任一 hunk 失败则整体不变。
func Apply(a, p []byte, opts Options) ([]byte, error) {
	pp, err := check(p, opts)
	if err != nil {
		return nil, err
	}
	return applyParsed(lines.Split(a), pp, opts, false)
}

// Reverse 反向应用补丁（b→a）。
func Reverse(b, p []byte, opts Options) ([]byte, error) {
	pp, err := check(p, opts)
	if err != nil {
		return nil, err
	}
	return applyParsed(lines.Split(b), reverse(pp), opts, true)
}

func check(p []byte, opts Options) (udiff.Patch, error) {
	if opts.MaxSize > 0 && len(p) > opts.MaxSize {
		return udiff.Patch{}, &ApplyError{Cat: CatFormat, Reason: "patch exceeds byte limit"}
	}
	pp, err := udiff.Parse(p)
	if err != nil {
		var fe *udiff.FormatError
		if errors.As(err, &fe) {
			return udiff.Patch{}, &ApplyError{Cat: CatFormat, Reason: fe.Error()}
		}
		return udiff.Patch{}, &ApplyError{Cat: CatFormat, Reason: err.Error()}
	}
	if opts.MaxHunk > 0 && len(pp.Hunks) > opts.MaxHunk {
		return udiff.Patch{}, &ApplyError{Cat: CatFormat, Reason: "patch exceeds hunk limit"}
	}
	return pp, nil
}

func reverse(p udiff.Patch) udiff.Patch {
	q := udiff.Patch{}
	for _, h := range p.Hunks {
		nh := udiff.Hunk{OldStart: h.NewStart, OldCount: h.NewCount,
			NewStart: h.OldStart, NewCount: h.OldCount}
		for _, e := range h.Body {
			k := e.Kind
			if k == udiff.Removed {
				k = udiff.Added
			} else if k == udiff.Added {
				k = udiff.Removed
			}
			nh.Body = append(nh.Body, udiff.Entry{Kind: k, Line: e.Line, NoNL: e.NoNL})
		}
		q.Hunks = append(q.Hunks, nh)
	}
	return q
}

type placement struct {
	idx     int // 目标行序列中的插入/覆盖起点（0 起）
	oldLen  int // 覆盖的旧行数
	entries []udiff.Entry
}

func applyParsed(src []lines.Line, p udiff.Patch, opts Options, rev bool) ([]byte, error) {
	var pl []placement
	offset := 0
	for hi, h := range p.Hunks {
		center := h.OldStart - 1 + offset
		best, bestDist := -1, 0
		lo, hi0 := center-opts.Fuzz, center+opts.Fuzz
		if lo < 0 {
			lo = 0
		}
		if hi0 > len(src) {
			hi0 = len(src)
		}
		for pos := lo; pos <= hi0; pos++ {
			if matchAt(src, h.Body, pos) {
				d := abs(pos - center)
				if best < 0 || d < bestDist || (d == bestDist && pos < best) {
					best, bestDist = pos, d
				}
			}
		}
		if best < 0 {
			cat, why := CatOffset, "no match within fuzz range"
			if exactElsewhere(src, h.Body, center, opts.Fuzz) {
				cat, why = CatContext, "context mismatch at recorded position"
			}
			return nil, &ApplyError{Cat: cat, Hunk: hi, Reason: why}
		}
		offset = (best + 1) - h.OldStart
		pl = append(pl, placement{idx: best, oldLen: h.OldCount, entries: h.Body})
	}
	out := append([]lines.Line(nil), src...)
	shift := 0
	for _, pc := range pl {
		pos := pc.idx + shift
		var add []lines.Line
		for _, e := range pc.entries {
			if e.Kind == udiff.Removed {
				continue
			}
			add = append(add, lines.Line{Data: append([]byte(nil), e.Line.Data...),
				NL: append([]byte(nil), e.Line.NL...)})
		}
		out = append(out[:pos], append(add, out[pos+pc.oldLen:]...)...)
		shift += len(add) - pc.oldLen
	}
	return lines.Join(out), nil
}

func matchAt(src []lines.Line, body []udiff.Entry, pos int) bool {
	i := pos
	for _, e := range body {
		switch e.Kind {
		case udiff.Added:
			continue
		case udiff.Context, udiff.Removed:
			if i >= len(src) {
				return false
			}
			got := src[i]
			if !lines.Equal(got, e.Line) {
				return false
			}
			i++
		}
	}
	return true
}

func exactElsewhere(src []lines.Line, body []udiff.Entry, center, fuzz int) bool {
	for pos := 0; pos <= len(src); pos++ {
		if pos >= center-fuzz && pos <= center+fuzz {
			continue
		}
		if matchAt(src, body, pos) {
			return true
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
