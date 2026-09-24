// Package patch 在文本上应用统一格式补丁，并提供多文档并发存储。
package patch

import (
	"errors"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

var (
	ErrFormat       = errors.New("malformed patch")
	ErrContext      = errors.New("context mismatch")
	ErrOutOfFuzz    = errors.New("hunk location outside fuzz range")
	ErrTooDifferent = errors.New("edit distance exceeds limit")
)

// HunkError 指出失败的 hunk 序号（0 基）与原因类别（errors.Is 可判定）。
type HunkError struct {
	Hunk   int
	Reason error
}

func (e *HunkError) Error() string { return "hunk " + itoa(e.Hunk) + ": " + e.Reason.Error() }
func (e *HunkError) Unwrap() error { return e.Reason }

// Options 配置应用：Fuzz 为记录位置上下允许偏移的行数；MaxBytes/MaxHunks 为资源上限。
type Options struct {
	Fuzz     int
	MaxBytes int
	MaxHunks int
}

// DiffConfig 配置补丁生成：Context 上下文行，MaxD 编辑距离上限。
type DiffConfig struct {
	Context int
	MaxD    int
}

// Diff 生成 a→b 的统一格式补丁。
func Diff(a, b []byte, cfg DiffConfig) ([]byte, error) {
	la, lb := lines.Split(a), lines.Split(b)
	d := &edit.Differ{}
	ops, err := d.Diff(la, lb, edit.Options{MaxD: cfg.MaxD})
	if err != nil {
		return nil, ErrTooDifferent
	}
	hs := hunk.Group(ops, cfg.Context)
	return udiff.Build(hs, la, lb).Render(), nil
}

// Reverse 返回反向补丁字节串（格式错误返回 ErrFormat）。
func Reverse(p []byte) ([]byte, error) {
	pp, err := udiff.Parse(p, 0)
	if err != nil {
		return nil, ErrFormat
	}
	out := &udiff.Patch{}
	for _, hh := range pp.Hunks {
		rh := udiff.Hunk{OldStart: hh.NewStart, OldCount: hh.NewCount, NewStart: hh.OldStart, NewCount: hh.OldCount}
		for i := len(hh.Rows) - 1; i >= 0; i-- {
			r := hh.Rows[i]
			k := r.Kind
			if k == '-' {
				k = '+'
			} else if k == '+' {
				k = '-'
			}
			rh.Rows = append(rh.Rows, udiff.Row{Kind: k, Line: r.Line, NoNLOld: r.NoNLNew, NoNLNew: r.NoNLOld})
		}
		out.Hunks = append(out.Hunks, rh)
	}
	return out.Render(), nil
}

// Apply 把补丁应用到 text；任何 hunk 失败整体不变。
func Apply(text, p []byte, opts Options) ([]byte, error) {
	if opts.MaxBytes > 0 && len(p) > opts.MaxBytes {
		return nil, ErrFormat
	}
	pp, err := udiff.Parse(p, opts.MaxHunks)
	if err != nil {
		return nil, ErrFormat
	}
	ls := lines.Split(text)
	out, _, err := applyAll(ls, pp.Hunks, opts.Fuzz)
	if err != nil {
		return nil, err
	}
	return lines.Join(out), nil
}

func applyAll(ls []lines.Line, hs []udiff.Hunk, fuzz int) ([]lines.Line, int, error) {
	cur := ls
	shift := 0
	for hi, h := range hs {
		oldRows, newRows := splitRows(h.Rows)
		center := h.OldStart - 1 + shift // 记录起点（0 基）+ 累计净变化
		pos, reason := locate(cur, oldRows, center, fuzz)
		if reason != nil {
			return nil, 0, &HunkError{Hunk: hi, Reason: reason}
		}
		cur = replace(cur, pos, len(oldRows), newRows)
		shift += len(newRows) - len(oldRows)
	}
	return cur, shift, nil
}

func splitRows(rows []udiff.Row) (oldRows, newRows []lines.Line) {
	for _, r := range rows {
		if r.Kind != '+' {
			oldRows = append(oldRows, r.Line)
		}
		if r.Kind != '-' {
			newRows = append(newRows, r.Line)
		}
	}
	return
}

func locate(ls, want []lines.Line, center, fuzz int) (int, error) {
	lo, hi := center-fuzz, center+fuzz
	if lo < 0 {
		lo = 0
	}
	if hi > len(ls) {
		hi = len(ls)
	}
	if lo > hi {
		return 0, ErrOutOfFuzz
	}
	best := -1
	for p := lo; p <= hi; p++ {
		if p+len(want) > len(ls) {
			continue
		}
		if eqSeg(ls[p:p+len(want)], want) && (best < 0 || abs(p-center) < abs(best-center)) {
			best = p
		}
	}
	if best < 0 {
		return 0, ErrContext
	}
	return best, nil
}

func eqSeg(a, b []lines.Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !lines.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func replace(ls []lines.Line, pos, n int, ins []lines.Line) []lines.Line {
	cp := append([]lines.Line(nil), ls[:pos]...)
	cp = append(cp, ins...)
	cp = append(cp, ls[pos+n:]...)
	return cp
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}
