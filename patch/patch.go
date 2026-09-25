// Package patch 把解析出的统一 diff 应用到文本上，并提供多文档并发存储。
package patch

import (
	"errors"
	"fmt"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// ErrContext 是 hunk 的上下文/删除行在目标中无法匹配的错误。
var ErrContext = errors.New("patch: context mismatch")

// ErrOutOfFuzz 是期望位置周围 F 行内不存在任何插入点的错误。
var ErrOutOfFuzz = errors.New("patch: hunk outside fuzz range")

// ApplyError 指出失败 hunk 的序号（0 起）与原因类别。
type ApplyError struct {
	HunkIndex int
	Reason    error // ErrContext 或 ErrOutOfFuzz
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("patch: hunk %d failed: %v", e.HunkIndex+1, e.Reason)
}

// Unwrap 支持 errors.Is 判断类别。
func (e *ApplyError) Unwrap() error { return e.Reason }

// Options 控制偏移搜索半径 F（0 表示只允许记录位置）。
type Options struct{ Fuzz int }

// Apply 把补丁应用到 a；任一 hunk 失败则整体不生效并返回 *ApplyError。
func Apply(a []byte, f *udiff.File, opt Options) ([]byte, error) {
	return apply(a, f, opt, false)
}

// Reverse 把补丁反向应用到 b，逐字节还原旧文本。
func Reverse(b []byte, f *udiff.File, opt Options) ([]byte, error) {
	return apply(b, f, opt, true)
}

func apply(src []byte, f *udiff.File, opt Options, rev bool) ([]byte, error) {
	cur := lines.Split(src)
	shift := 0
	for idx := range f.Hunks {
		hh := f.Hunks[idx]
		if rev {
			hh = reverseHunk(hh)
		}
		want := hh.OldStart - 1 + shift
		pos, err := locate(cur, hh, want, opt.Fuzz)
		if err != nil {
			return nil, &ApplyError{HunkIndex: idx, Reason: err}
		}
		cur = splice(cur, pos, hh)
		shift += hh.NewCount - hh.OldCount
	}
	return lines.Join(cur), nil
}

// locate 在 want±fuzz 内寻找精确匹配 hunk 旧侧的位置；距离最近优先，并列靠前。
func locate(cur []lines.Line, hh hunk.Hunk, want, fuzz int) (int, error) {
	best, bestDist := -1, fuzz+1
	lo := want - fuzz
	if lo < 0 {
		lo = 0
	}
	hi := want + fuzz
	if hi > len(cur) {
		hi = len(cur)
	}
	for p := lo; p <= hi; p++ {
		if matches(cur, p, hh) {
			d := p - want
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				best, bestDist = p, d
			}
		}
	}
	if best >= 0 {
		return best, nil
	}
	if want+fuzz < 0 || want-fuzz > len(cur) {
		return 0, ErrOutOfFuzz
	}
	return 0, ErrContext
}

func oldLines(hh hunk.Hunk) []lines.Line {
	out := make([]lines.Line, 0, hh.OldCount)
	for _, op := range hh.Ops {
		if op.Kind == edit.Equal || op.Kind == edit.Delete {
			out = append(out, op.L)
		}
	}
	return out
}

func newLines(hh hunk.Hunk) []lines.Line {
	out := make([]lines.Line, 0, hh.NewCount)
	for _, op := range hh.Ops {
		if op.Kind == edit.Equal || op.Kind == edit.Insert {
			out = append(out, op.L)
		}
	}
	return out
}

func matches(cur []lines.Line, pos int, hh hunk.Hunk) bool {
	old := oldLines(hh)
	if pos < 0 || pos+len(old) > len(cur) {
		return false
	}
	for i, l := range old {
		if cur[pos+i].Content() != l.Content() {
			return false
		}
	}
	return true
}

func splice(cur []lines.Line, pos int, hh hunk.Hunk) []lines.Line {
	old, neo := oldLines(hh), newLines(hh)
	out := make([]lines.Line, 0, len(cur)-len(old)+len(neo))
	out = append(out, cur[:pos]...)
	out = append(out, neo...)
	out = append(out, cur[pos+len(old):]...)
	return out
}

func reverseHunk(hh hunk.Hunk) hunk.Hunk {
	rh := hh
	rh.OldStart, rh.NewStart = hh.NewStart, hh.OldStart
	rh.OldCount, rh.NewCount = hh.NewCount, hh.OldCount
	for i, op := range hh.Ops {
		switch op.Kind {
		case edit.Delete:
			op.Kind = edit.Insert
		case edit.Insert:
			op.Kind = edit.Delete
		}
		rh.Ops[i] = op
	}
	return rh
}
