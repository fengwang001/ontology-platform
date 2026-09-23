// Package patch 把统一格式补丁应用到文本上（偏移查找、原子拒绝、反向应用），
// 并提供带版本号的多文档并发存储。
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// 可判定的失败类别。
var (
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: match exists but beyond offset range")
)

// HunkError 指出第几个 hunk（0 基）失败及原因类别。
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("patch: hunk %d: %s", e.Index, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Options 控制应用行为；零值表示 Fuzz=0、不设规模上限。
type Options struct {
	Fuzz     int // 记录位置上下允许查找的行数
	MaxBytes int // 补丁总字节数上限，0 不限
	MaxHunks int // hunk 数上限，0 不限
}

// Apply 把补丁应用到 text；任一 hunk 失败则整体不生效并返回错误。
func Apply(text, patch []byte, o *Options) ([]byte, error) { return apply(text, patch, o, false) }

// Reverse 反向应用补丁（把新文本还原为旧文本）。
func Reverse(text, patch []byte, o *Options) ([]byte, error) { return apply(text, patch, o, true) }

func apply(text, p []byte, o *Options, rev bool) ([]byte, error) {
	var opt Options
	if o != nil {
		opt = *o
	}
	parsed, err := udiff.Parse(p, udiff.Limits{MaxBytes: opt.MaxBytes, MaxHunks: opt.MaxHunks})
	if err != nil {
		return nil, err
	}
	src := lines.Split(text)
	pos := make([]int, len(parsed.Hunks))
	shift := 0
	for i := range parsed.Hunks {
		h := &parsed.Hunks[i]
		want, start, _ := side(h, rev)
		at, err := locate(src, want, start+shift, opt.Fuzz)
		if err != nil {
			return nil, &HunkError{i, err}
		}
		pos[i] = at
		_, _, repl := side(h, rev)
		shift = at - start + len(repl) - len(want)
	}
	var out []string
	cur := 0
	for i := range parsed.Hunks {
		h := &parsed.Hunks[i]
		want, _, repl := side(h, rev)
		out = append(out, src[cur:pos[i]]...)
		out = append(out, repl...)
		cur = pos[i] + len(want)
	}
	out = append(out, src[cur:]...)
	return lines.Join(out), nil
}

// side 返回该 hunk 要匹配的旧侧行、起始下标与替换行；rev 时交换新旧。
func side(h *hunk.Hunk, rev bool) (want []string, start int, repl []string) {
	os, ns := h.OldStart, h.NewStart
	if rev {
		os, ns = ns, os
	}
	for _, l := range h.Lines {
		oldSide := l.Kind != '+'
		if oldSide != rev {
			want = append(want, l.Text)
		}
		if oldSide == rev {
			repl = append(repl, l.Text)
		}
	}
	return want, os, repl
}

// locate 在 [center-fuzz, center+fuzz] 内找 want 的精确匹配，
// 多个候选取离 center 最近者，距离相同取靠前者。
func locate(src, want []string, center, fuzz int) (int, error) {
	best, bestDist, foundAny := -1, 0, false
	for p := 0; p+len(want) <= len(src); p++ {
		if !matchAt(src, want, p) {
			continue
		}
		foundAny = true
		d := abs(p - center)
		if d <= fuzz && (best < 0 || d < bestDist) {
			best, bestDist = p, d
		}
	}
	if best >= 0 {
		return best, nil
	}
	if foundAny {
		return -1, ErrOffset
	}
	return -1, ErrContext
}

func matchAt(src, want []string, p int) bool {
	for i, w := range want {
		if src[p+i] != w {
			return false
		}
	}
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Commit 是存储提交日志中的一次成功应用。
type Commit struct {
	ID      string
	Version int
	Patch   []byte
}

type doc struct {
	text    []byte
	version int
}

// Store 是多文档内存存储，每次成功应用原子替换并版本加一。
type Store struct {
	mu   sync.Mutex
	docs map[string]*doc
	log  []Commit
}

func NewStore() *Store { return &Store{docs: map[string]*doc{}} }

// Put 以版本 0 写入文档初始内容。
func (s *Store) Put(id string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[id] = &doc{text: text}
}

// Apply 在一致快照上应用补丁：成功则原子替换、版本加一并记入提交日志；
// 失败则状态零变化。
func (s *Store) Apply(id string, p []byte, o *Options) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[id]
	next, err := Apply(d.text, p, o)
	if err != nil {
		return d.version, err
	}
	d.text = next
	d.version++
	s.log = append(s.log, Commit{ID: id, Version: d.version, Patch: p})
	return d.version, nil
}

// Get 返回文档当前文本与版本号。
func (s *Store) Get(id string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[id]
	return d.text, d.version
}

// Log 返回提交日志（按实际提交顺序）。
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
