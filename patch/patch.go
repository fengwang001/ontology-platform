// Package patch 把解析后的统一格式补丁应用到内存文本，支持偏移查找、原子拒绝、反向与并发应用。
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/lines"
	"ontology/udiff"
)

var (
	// ErrFormat 补丁本身格式错误（解析阶段）。
	ErrFormat = errors.New("patch: malformed patch")
	// ErrContext 某 hunk 在候选位置上下文/删除行不匹配。
	ErrContext = errors.New("patch: context mismatch")
	// ErrOffset 某 hunk 在允许偏移范围内找不到任何匹配。
	ErrOffset = errors.New("patch: no match within fuzz")
)

// Error 指出失败的 hunk 序号（0 基）与原因类别。
type Error struct {
	Hunk int
	Kind error // ErrContext / ErrOffset
}

func (e *Error) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Hunk+1, e.Kind) }
func (e *Error) Unwrap() error { return e.Kind }

// Options 配置应用过程。
type Options struct {
	Fuzz int // 记录位置上下允许搜索的行数
}

// Apply 把补丁文本应用到 a；任何 hunk 失败则返回错误且结果为 nil（原子拒绝）。
func Apply(a []byte, ptext []byte, opt Options) ([]byte, error) {
	return applyOne(a, ptext, opt, false)
}

// Reverse 把补丁反向应用到 b，逐字节还原 a。
func Reverse(b []byte, ptext []byte, opt Options) ([]byte, error) {
	return applyOne(b, ptext, opt, true)
}

func applyOne(src []byte, ptext []byte, opt Options, rev bool) ([]byte, error) {
	p, err := udiff.Parse(ptext, udiff.Limits{})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	cur := lines.Split(src)
	consumed := 0
	shift := 0
	for hi, h0 := range p.Hunks {
		h := h0
		if rev {
			h = reverseHunk(h0)
		}
		origin := h.OldStart - 1 + shift
		pos, found := locate(cur, h, origin, opt.Fuzz, consumed)
		if !found {
			kind := ErrOffset
			if anyCandidate(h, origin, opt.Fuzz, consumed, len(cur)) {
				kind = ErrContext
			}
			return nil, &Error{Hunk: hi, Kind: kind}
		}
		cur = splice(cur, h, pos)
		consumed = pos + h.NewCount
		shift = pos - (h.OldStart - 1)
	}
	return lines.Join(cur), nil
}

func reverseHunk(h *udiff.Hunk) *udiff.Hunk {
	r := &udiff.Hunk{OldStart: h.NewStart, OldCount: h.NewCount, NewStart: h.OldStart, NewCount: h.OldCount}
	for _, row := range h.Rows {
		k := row.Kind
		switch k {
		case '-':
			k = '+'
		case '+':
			k = '-'
		}
		r.Rows = append(r.Rows, udiff.Row{Kind: k, Line: lines.Clone(row.Line), NoNL: row.NoNL})
	}
	return r
}

func locate(cur []lines.Line, h *udiff.Hunk, origin, fuzz, minPos int) (int, bool) {
	for d := 0; d <= fuzz; d++ {
		for _, cand := range candidates(origin, d) {
			if cand < minPos || cand+h.OldCount > len(cur) {
				continue
			}
			if matchesAt(cur, h, cand) {
				return cand, true
			}
		}
	}
	return 0, false
}

func candidates(origin, d int) []int {
	if d == 0 {
		return []int{origin}
	}
	return []int{origin - d, origin + d}
}

func anyCandidate(h *udiff.Hunk, origin, fuzz, minPos, n int) bool {
	for d := 0; d <= fuzz; d++ {
		for _, cand := range []int{origin - d, origin + d} {
			if cand >= minPos && cand+h.OldCount <= n {
				return true
			}
		}
	}
	return false
}

func matchesAt(cur []lines.Line, h *udiff.Hunk, pos int) bool {
	j := pos
	for _, r := range h.Rows {
		if r.Kind == '+' {
			continue
		}
		if j >= len(cur) || !lines.Equal(cur[j], r.Line) {
			return false
		}
		j++
	}
	return true
}

func splice(cur []lines.Line, h *udiff.Hunk, pos int) []lines.Line {
	out := make([]lines.Line, 0, len(cur)-h.OldCount+h.NewCount)
	out = append(out, cur[:pos]...)
	for _, r := range h.Rows {
		if r.Kind == '-' {
			continue
		}
		out = append(out, lines.Clone(r.Line))
	}
	out = append(out, cur[pos+h.OldCount:]...)
	return out
}

// Commit 是一条成功应用记录，供串行重放。
type Commit struct{ Patch []byte }

// Doc 是一个带版本号的文档；所有状态都在进程内存。
type Doc struct {
	mu      sync.Mutex
	text    []byte
	version int
	log     []Commit
}

// NewDoc 创建版本号为 0 的文档。
func NewDoc(text []byte) *Doc { return &Doc{text: append([]byte(nil), text...)} }

// Snapshot 返回当前文本与版本号的拷贝。
func (d *Doc) Snapshot() ([]byte, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.text...), d.version
}

// Apply 在一致快照上判定并原子替换；失败时状态零变化。
func (d *Doc) Apply(ptext []byte, opt Options) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	next, err := applyOne(d.text, ptext, opt, false)
	if err != nil {
		return d.version, err
	}
	d.text = next
	d.version++
	d.log = append(d.log, Commit{Patch: append([]byte(nil), ptext...)})
	return d.version, nil
}

// Replay 从初始文本按提交日志串行重放，返回最终文本。
func Replay(initial []byte, log []Commit, opt Options) ([]byte, error) {
	cur := append([]byte(nil), initial...)
	var err error
	for _, c := range log {
		if cur, err = applyOne(cur, c.Patch, opt, false); err != nil {
			return nil, err
		}
	}
	return cur, nil
}

// Log 返回提交日志拷贝。
func (d *Doc) Log() []Commit {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Commit, len(d.log))
	copy(out, d.log)
	return out
}
