// Package patch 把统一格式补丁应用到文本，并提供多文档并发存储。
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// 四类可判定错误，彼此可用 errors.Is 区分。
var (
	ErrFormat      = udiff.ErrFormat // 补丁本身格式错误
	ErrContext     = errors.New("patch: context mismatch")
	ErrOutOfRange  = errors.New("patch: offset outside fuzz range")
	ErrTooDifferent = errors.New("patch: sources differ too much")
)

// ApplyError 指出第几个 hunk（0 基）与原因类别。
type ApplyError struct {
	Hunk  int
	Cause error
}

func (e *ApplyError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Hunk, e.Cause) }
func (e *ApplyError) Unwrap() error { return e.Cause }

// Options 控制差分距离上限与偏移窗口 F。F<=0 表示不允许偏移。
type Options struct {
	Context int
	MaxD    int
	Fuzz    int
}

// Diff 对 a、b 做差分并渲染成统一格式补丁文本（文件名固定 a/b）。
func Diff(a, b string, o Options) (string, error) {
	al, bl := lines.Split(a), lines.Split(b)
	s, err := edit.Diff(al, bl, o.MaxD)
	if err != nil {
		if errors.Is(err, edit.ErrTooDifferent) {
			return "", ErrTooDifferent
		}
		return "", err
	}
	araw, braw := raws(al), raws(bl)
	hs := hunk.Build(s, araw, braw, o.Context)
	ph := make([]*hunk.Hunk, len(hs))
	for i := range hs {
		ph[i] = &hs[i]
	}
	p := &udiff.Patch{OldName: "a", NewName: "b", Hunks: ph}
	return udiff.Render(p, o.Context), nil
}

// LastSteps 返回最近一次差分对角线前进步数（委托 edit 计数器的便捷入口见
// edit.Script.Steps；本函数重算一次并返回计数，供复杂度测试使用）。
func LastSteps(a, b string, maxD int) (int64, error) {
	s, err := edit.Diff(lines.Split(a), lines.Split(b), maxD)
	if err != nil {
		return s.Steps, err
	}
	return s.Steps, nil
}

// Apply 把补丁文本应用到 src：全部 hunk 在一致快照上匹配成功后才生成结果，
// 任一 hunk 失败则返回 *ApplyError，结果必须视同未发生。
func Apply(src, text string, o Options) (string, error) {
	p, err := udiff.Parse(text, udiff.Options{})
	if err != nil {
		return "", err
	}
	cur := lines.Split(src)
	var out []lines.Line
	pos, shift := 0, 0
	for hi, hh := range p.Hunks {
		oldWant := hunkOldLines(hh)
		ins := hunkNewLines(hh)
		base := hh.OStart - 1 + shift
		idx, ok := locate(cur, oldWant, base, o.Fuzz)
		if !ok {
			if outOfRange(cur, oldWant, base, o.Fuzz) {
				return "", &ApplyError{Hunk: hi, Cause: ErrOutOfRange}
			}
			return "", &ApplyError{Hunk: hi, Cause: ErrContext}
		}
		out = append(out, cur[pos:idx]...)
		out = append(out, ins...)
		pos = idx + len(oldWant)
		shift = pos - hh.OStart - hh.OCount
	}
	out = append(out, cur[pos:]...)
	return lines.Join(out), nil
}

// Reverse 返回交换新旧两侧后的补丁文本；用于反向应用。
func Reverse(text string) (string, error) {
	p, err := udiff.Parse(text, udiff.Options{})
	if err != nil {
		return "", err
	}
	for _, hh := range p.Hunks {
		hh.OStart, hh.NStart = hh.NStart, hh.OStart
		hh.OCount, hh.NCount = hh.NCount, hh.OCount
		hh.OldNoNL, hh.NewNoNL = hh.NewNoNL, hh.OldNoNL
		for i := range hh.Items {
			it := &hh.Items[i]
			it.OL, it.NL = it.NL, it.OL
			switch it.Kind {
			case hunk.Remove:
				it.Kind = hunk.Add
			case hunk.Add:
				it.Kind = hunk.Remove
			}
		}
	}
	p.OldName, p.NewName = p.NewName, p.OldName
	return udiff.Render(p, 0), nil
}

// locate 在 cur 中找与 want 精确相等的位置：优先 base，再在 [base-F,base+F]
// 窗口内取离 base 最近、同距靠前者；窗口外一律不接受。
func locate(cur, want []lines.Line, base, fuzz int) (int, bool) {
	for d := 0; ; d++ {
		for si, cand := range []int{base - d, base + d} {
			if d == 0 && si == 1 {
				continue
			}
			if cand < 0 || cand+len(want) > len(cur) {
				continue
			}
			if equalLines(cur[cand:cand+len(want)], want) {
				return cand, true
			}
		}
		if d >= fuzz {
			return 0, false
		}
	}
}

func outOfRange(cur, want []lines.Line, base, fuzz int) bool {
	lo, hi := base-fuzz, base+fuzz
	if lo < 0 {
		lo = 0
	}
	if hi+len(want) > len(cur) {
		hi = len(cur) - len(want)
	}
	for p := lo; p <= hi; p++ {
		if equalLines(cur[p:p+len(want)], want) {
			return false
		}
	}
	return true
}

func equalLines(a, b []lines.Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hunkOldLines(h *hunk.Hunk) []lines.Line {
	var out []lines.Line
	for _, it := range h.Items {
		if it.Kind != hunk.Add {
			out = append(out, lines.Split(it.OL)...)
		}
	}
	return out
}

func hunkNewLines(h *hunk.Hunk) []lines.Line {
	var out []lines.Line
	for _, it := range h.Items {
		if it.Kind != hunk.Remove {
			out = append(out, lines.Split(it.NL)...)
		}
	}
	return out
}

func raws(ls []lines.Line) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Raw()
	}
	return out
}

// Commit 是提交日志中的一条成功应用记录。
type Commit struct {
	Doc    int
	Result string
}

// Doc 是带版本号的文档。
type Doc struct {
	Text string
	Ver  int
}

// Store 是进程内存中的多文档存储，Apply 串行化以保证快照一致与版本原子递增。
type Store struct {
	mu   sync.Mutex
	docs map[int]*Doc
	log  []Commit
}

// NewStore 以 id→初始文本建立存储，所有文档版本初始为 0。
func NewStore(init map[int]string) *Store {
	s := &Store{docs: map[int]*Doc{}}
	for id, t := range init {
		s.docs[id] = &Doc{Text: t}
	}
	return s
}

// Apply 对文档 id 在持锁快照上应用补丁；成功则原子替换、版本加一、写日志，
// 失败则状态零变化。ok 报告是否成功。
func (s *Store) Apply(id int, text string, o Options) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[id]
	if d == nil {
		return false
	}
	res, err := Apply(d.Text, text, o)
	if err != nil {
		return false
	}
	d.Text = res
	d.Ver++
	s.log = append(s.log, Commit{Doc: id, Result: res})
	return true
}

// Snapshot 返回文档文本与版本的副本。
func (s *Store) Snapshot(id int) (Doc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[id]
	if d == nil {
		return Doc{}, false
	}
	return *d, true
}

// Log 返回提交日志副本。
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
