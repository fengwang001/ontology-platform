// Package patch 应用统一格式补丁（偏移查找、原子拒绝、反向应用）并提供多文档并发存储。
package patch

import (
	"bytes"
	"errors"
	"fmt"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
	"sync"
)

var ErrContext = errors.New("patch: context mismatch")           // 偏移范围内无精确匹配
var ErrOffset = errors.New("patch: match outside offset window") // 匹配存在但超出偏移范围

// HunkError 指出第几个 hunk（1-based）失败及原因类别。
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("hunk %d: %v", e.Index, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Apply 把补丁 p 应用到 src（offset 为查找半径）；任一 hunk 失败则整体不生效。
func Apply(src []byte, p *udiff.Patch, offset int) ([]byte, error) { return apply(src, p, offset) }

// Reverse 反向应用补丁（把新文本还原为旧文本）。
func Reverse(src []byte, p *udiff.Patch, offset int) ([]byte, error) {
	return apply(src, p.Invert(), offset)
}
func apply(src []byte, p *udiff.Patch, offset int) ([]byte, error) {
	old := lines.Split(src)
	pos := make([]int, len(p.Hunks))
	delta := 0 // 前一 hunk 的位移（实际位置 − 记录位置），见 DESIGN.md 第 4 节
	for i, h := range p.Hunks {
		start := h.OldStart - 1
		if h.OldCount == 0 {
			start = h.OldStart // 零计数：插入点在 0-based 下标 a 处
		}
		at, ok := find(old, h, start+delta, offset)
		if !ok {
			err := ErrContext
			if _, anywhere := find(old, h, start+delta, len(old)+1); anywhere {
				err = ErrOffset
			}
			return nil, &HunkError{Index: i + 1, Err: err}
		}
		pos[i], delta = at, at-start
	}
	var out [][]byte
	cur := 0
	for i, h := range p.Hunks {
		out = append(out, old[cur:pos[i]]...)
		for _, l := range h.Lines {
			if l.Kind != '-' {
				out = append(out, l.Text)
			}
		}
		cur = pos[i] + h.OldCount
	}
	return lines.Join(append(out, old[cur:]...)), nil
}

// find 在 center 上下 fuzz 行内寻找精确匹配 hunk 旧侧的位置；多候选取最近，同距取靠前。
func find(old [][]byte, h hunk.Hunk, center, fuzz int) (int, bool) {
	match := func(at int) bool {
		i := at
		for _, l := range h.Lines {
			if l.Kind == '+' {
				continue
			}
			if i < 0 || i >= len(old) || !bytes.Equal(old[i], l.Text) {
				return false
			}
			i++
		}
		return true
	}
	for d := 0; d <= fuzz; d++ {
		if match(center - d) {
			return center - d, true
		}
		if d > 0 && match(center+d) {
			return center + d, true
		}
	}
	return 0, false
}

// Commit 是提交日志的记录。
type Commit struct {
	Doc     string
	Version int
	Patch   *udiff.Patch
}

// Store 是多文档并发存储：版本号即该文档提交日志长度，补丁应用原子生效。
type Store struct {
	mu     sync.Mutex
	docs   map[string][]byte
	log    map[string][]Commit
	Limits udiff.Limits // 解析上限
	Offset int          // 查找半径
}

// NewStore 创建空存储；lim 为补丁解析上限，offset 为查找半径。
func NewStore(lim udiff.Limits, offset int) *Store {
	return &Store{docs: map[string][]byte{}, log: map[string][]Commit{}, Limits: lim, Offset: offset}
}

// Put 以版本 0 创建或覆盖文档。Get 返回当前文本与版本号。
func (s *Store) Put(name string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name], s.log[name] = append([]byte(nil), text...), nil
}

func (s *Store) Get(name string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.docs[name]...), len(s.log[name])
}

// Apply 解析并应用补丁：锁内基于一致快照判断，成功则原子替换、版本加一并记日志；失败状态零变化。
func (s *Store) Apply(name string, patchText []byte) (int, error) {
	p, err := udiff.Parse(patchText, s.Limits)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := Apply(s.docs[name], p, s.Offset)
	if err != nil {
		return 0, err
	}
	s.docs[name] = out
	s.log[name] = append(s.log[name], Commit{Doc: name, Version: len(s.log[name]) + 1, Patch: p})
	return len(s.log[name]), nil
}

// Log 返回文档的提交日志（按实际提交顺序）。
func (s *Store) Log(name string) []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log[name]...)
}
