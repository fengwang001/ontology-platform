// Package patch 把解析出的补丁应用到文本上，并提供多文档并发存储。
package patch

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

var ErrContext = errors.New("上下文不匹配") // 任何位置都找不到上下文/删除行
var ErrRange = errors.New("超出偏移范围")   // 匹配存在但超出 ±fuzz 范围

// Apply 把 p 应用到 src，fuzz 是允许的最大偏移行数；任一 hunk 失败则整体不生效。
func Apply(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return apply(src, p, fuzz, false)
}

// Reverse 反向应用 p（把新文本还原为旧文本）。
func Reverse(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return apply(src, p, fuzz, true)
}

func apply(src []byte, p *udiff.Patch, fuzz int, rev bool) ([]byte, error) {
	ls := lines.Split(src)
	out := make([][]byte, 0, len(ls))
	pos, delta := 0, 0 // pos: 已消费到的下标；delta: 累计平移（见 DESIGN.md 第 4 节）
	for hi, h := range p.Hunks {
		start, oldN := h.AStart, h.ACount
		if rev {
			start, oldN = h.BStart, h.BCount
		}
		idx := start + delta
		if oldN > 0 {
			idx--
		}
		at, err := locate(ls, idx, oldN, side(h, rev), fuzz)
		if err == nil && at < pos {
			err = ErrContext
		}
		if err != nil {
			return nil, fmt.Errorf("patch: 第 %d 个 hunk: %w", hi+1, err)
		}
		out = append(out, ls[pos:at]...)
		for _, l := range h.Lines {
			if l.Kind == ' ' || (l.Kind == '+') != rev {
				out = append(out, l.Text)
			}
		}
		pos = at + oldN
		delta += at - idx // 按原文坐标查找，只传播落点偏移（DESIGN.md 第 4 节）
	}
	return lines.Join(append(out, ls[pos:]...)), nil
}

// side 返回 hunk 的匹配侧（旧侧；rev 时为新侧）行序列。
func side(h hunk.Hunk, rev bool) [][]byte {
	var out [][]byte
	for _, l := range h.Lines {
		if l.Kind == ' ' || (l.Kind == '-') != rev {
			out = append(out, l.Text)
		}
	}
	return out
}

// locate 在 idx 上下 fuzz 行内找精确匹配，取最近者，距离相同取靠前者。
func locate(ls [][]byte, idx, n int, want [][]byte, fuzz int) (int, error) {
	best, bestD := -1, 1<<30
	lo, hi := max(idx-fuzz, 0), min(idx+fuzz, len(ls)-n)
	for at := lo; at <= hi; at++ {
		if !match(ls, at, want) {
			continue
		}
		d := at - idx
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = at, d
		}
	}
	if best >= 0 {
		return best, nil
	}
	for at := 0; at+n <= len(ls); at++ {
		if match(ls, at, want) {
			return -1, ErrRange
		}
	}
	return -1, ErrContext
}

func match(ls [][]byte, at int, want [][]byte) bool {
	return at >= 0 && at+len(want) <= len(ls) &&
		slices.EqualFunc(ls[at:at+len(want)], want, bytes.Equal)
}

// Commit 记录一次成功提交，用于按提交顺序串行重放。
type Commit struct {
	Doc   string
	Patch *udiff.Patch
	Fuzz  int
}

// Store 是多文档存储：每个文档带版本号，补丁应用是原子的。
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	vers map[string]int
	log  []Commit
}

func NewStore() *Store { return &Store{docs: map[string][]byte{}, vers: map[string]int{}} }

// Put 设置文档初始内容（版本不变）。
func (s *Store) Put(doc string, text []byte) { s.mu.Lock(); s.docs[doc] = text; s.mu.Unlock() }

// Apply 在一致快照上应用补丁：成功则原子替换并版本加一，失败零变化。
func (s *Store) Apply(doc string, p *udiff.Patch, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := Apply(s.docs[doc], p, fuzz)
	if err != nil {
		return s.vers[doc], err
	}
	s.docs[doc] = next
	s.vers[doc]++
	s.log = append(s.log, Commit{doc, p, fuzz})
	return s.vers[doc], nil
}

// Get 返回文档当前文本与版本号。
func (s *Store) Get(doc string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[doc], s.vers[doc]
}

// Log 返回按提交顺序排列的成功补丁日志。
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
