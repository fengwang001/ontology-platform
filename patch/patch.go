// Package patch 把 unified diff 补丁应用到文本，并提供带版本号的多文档并发存储。
package patch

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/lines"
	"ontology/udiff"
)

// 四类彼此可区分的错误，加上资源上限错误。
var (
	ErrFormat       = errors.New("patch: malformed patch")
	ErrContext      = errors.New("patch: context mismatch")
	ErrOffset       = errors.New("patch: offset search out of range")
	ErrTooDifferent = errors.New("patch: files too different")
	ErrTooLarge     = errors.New("patch: resource limit exceeded")
)

// Error 指出失败的 hunk 序号（0 基）与类别。
type Error struct {
	Hunk  int
	Kind  error
	Inner error
}

func (e *Error) Error() string {
	return fmt.Sprintf("patch: hunk %d: %v", e.Hunk, e.Inner)
}
func (e *Error) Unwrap() error { return e.Kind }

// Limits 是应用时的资源与搜索限制。
type Limits struct {
	Fuzz      int // 上下允许偏移的行数
	MaxBytes  int // 补丁文本最大字节数（0 不限）
	MaxHunks  int // hunk 数上限（0 不限）
}

// Apply 将 text 形式的补丁应用到 target；任何 hunk 失败则整体不变。
func Apply(target, text string, lim Limits) (string, error) {
	if err := checkSize(text, lim); err != nil {
		return target, err
	}
	p, err := udiff.Parse(text)
	if err != nil {
		return target, &Error{Hunk: -1, Kind: ErrFormat, Inner: err}
	}
	if lim.MaxHunks > 0 && len(p.Hunks) > lim.MaxHunks {
		return target, ErrTooLarge
	}
	return applyParsed(target, p, lim)
}

// ApplyParsed 应用已解析的补丁。
func ApplyParsed(target string, p *udiff.Patch, lim Limits) (string, error) {
	return applyParsed(target, p, lim)
}

func checkSize(text string, lim Limits) error {
	if lim.MaxBytes > 0 && len(text) > lim.MaxBytes {
		return ErrTooLarge
	}
	return nil
}

func applyParsed(target string, p *udiff.Patch, lim Limits) (string, error) {
	cur := lines.Split(target)
	out := append([]lines.Line(nil), cur...)
	shift := 0
	for hi, h := range p.Hunks {
		oldLines, newLines, oldN := extract(&h)
		base := h.OldStart - 1 + shift
		if h.OldCount == 0 {
			base = h.OldStart + shift
		}
		found, anyMatch := -1, false
		for d := 0; d <= lim.Fuzz; d++ {
			cands := []int{base + d}
			if d != 0 {
				cands = append(cands, base-d)
			}
			for _, pos := range cands {
				if pos < 0 || pos+oldN > len(out) {
					continue
				}
				if matchAt(out, pos, oldLines) {
					found = pos
					anyMatch = true
					goto done
				}
				anyMatch = anyMatch || exists(out, oldLines)
			}
		}
	done:
		if found < 0 {
			kind := ErrContext
			if anyMatch {
				kind = ErrOffset
			}
			return target, &Error{Hunk: hi, Kind: kind, Inner: kind}
		}
		out = append(append(append([]lines.Line(nil), out[:found]...), newLines...), out[found+oldN:]...)
		shift += len(newLines) - oldN
	}
	return lines.Join(out), nil
}

func extract(h *udiff.Hunk) (old, neu []lines.Line, n int) {
	for _, l := range h.Lines {
		text, term := splitTerm(l.Text, true)
		if l.Kind == ' ' {
			t := term
			if l.OldNL {
				t = ""
			}
			old = append(old, lines.Line{Text: text, Term: t})
			t = term
			if l.NewNL {
				t = ""
			}
			neu = append(neu, lines.Line{Text: text, Term: t})
			n++
		} else if l.Kind == '-' {
			t := term
			if l.OldNL {
				t = ""
			}
			old = append(old, lines.Line{Text: text, Term: t})
			n++
		} else {
			t := term
			if l.NewNL {
				t = ""
			}
			neu = append(neu, lines.Line{Text: text, Term: t})
		}
	}
	return old, neu, n
}

func splitTerm(text string, newline bool) (string, string) {
	term := "\n"
	if strings.HasSuffix(text, "\r") {
		text = text[:len(text)-1]
		term = "\r\n"
	}
	if !newline {
		term = ""
	}
	return text, term
}

func matchAt(s []lines.Line, pos int, want []lines.Line) bool {
	for j, w := range want {
		if s[pos+j] != w {
			return false
		}
	}
	return true
}

func exists(s []lines.Line, want []lines.Line) bool {
	for pos := 0; pos+len(want) <= len(s); pos++ {
		if matchAt(s, pos, want) {
			return true
		}
	}
	return false
}

// Reverse 返回该补丁的反向补丁文本（用于从 b 回退到 a）。
func Reverse(text string) (string, error) {
	p, err := udiff.Parse(text)
	if err != nil {
		return "", err
	}
	for i := range p.Hunks {
		h := &p.Hunks[i]
		h.OldStart, h.NewStart = h.NewStart, h.OldStart
		h.OldCount, h.NewCount = h.NewCount, h.OldCount
		for j := range h.Lines {
			l := &h.Lines[j]
			l.OldNL, l.NewNL = l.NewNL, l.OldNL
			if l.Kind == '-' {
				l.Kind = '+'
			} else if l.Kind == '+' {
				l.Kind = '-'
			}
		}
	}
	return udiff.Render(p), nil
}

// Entry 是提交日志中的一条记录。
type Entry struct {
	Version int64
	Patch   string
}

// Doc 是一个带版本号的文档。
type Doc struct {
	text    string
	version int64
	log     []Entry
	mu      sync.Mutex
}

// NewDoc 创建文档，版本从 0 起。
func NewDoc(initial string) *Doc { return &Doc{text: initial} }

// Result 是一次应用的结果。
type Result struct {
	OK      bool
	Version int64
	Err     error
}

// Apply 在一致快照上尝试应用：成功则原子替换、版本加一并写日志；失败零变化。
func (d *Doc) Apply(text string, lim Limits) Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	out, err := Apply(d.text, text, lim)
	if err != nil {
		return Result{OK: false, Version: d.version, Err: err}
	}
	d.version++
	d.text = out
	d.log = append(d.log, Entry{Version: d.version, Patch: text})
	return Result{OK: true, Version: d.version}
}

// Snapshot 返回当前文本、版本与成功补丁的提交日志。
func (d *Doc) Snapshot() (string, int64, []Entry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	log := append([]Entry(nil), d.log...)
	return d.text, d.version, log
}
