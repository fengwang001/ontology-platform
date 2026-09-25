// Package trie 实现带引用计数剪枝的前缀树：插入、删除、前缀子树遍历。
package trie

import (
	"errors"
	"strconv"
	"sync"
	"unicode/utf8"
)

var ErrEmptyString = errors.New("trie: empty string")
var ErrInvalidUTF8 = errors.New("trie: invalid utf-8")
var ErrNotFound = errors.New("trie: string not found")

// UTF8Error 携带首个非法字节的偏移，可用 errors.As 取出。
type UTF8Error struct{ Offset int }

func (e *UTF8Error) Error() string { return "trie: invalid utf-8 at byte " + strconv.Itoa(e.Offset) }
func (e *UTF8Error) Unwrap() error { return ErrInvalidUTF8 }

// CheckString 校验空串与非法 UTF-8，不触碰任何状态。
func CheckString(s string) error {
	if s == "" {
		return ErrEmptyString
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return &UTF8Error{Offset: i}
		}
		i += size
	}
	return nil
}

// Candidate 是子树遍历产出的一条 (串, 频率)。
type Candidate struct {
	Word string
	Freq int
}

type node struct {
	children map[rune]*node
	terminal bool
	freq     int
	refs     int // 子树内终端串个数（引用计数）
}

// Trie 并发安全；visited 记录 Insert/Delete 访问的节点数（非导出，仅包内测试可读）。
type Trie struct {
	mu      sync.RWMutex
	root    *node
	count   int
	visited int
}

func New() *Trie { return &Trie{root: &node{}} }

func (t *Trie) walk(s string, create bool) ([]*node, []rune, bool) {
	path, rs := []*node{t.root}, []rune(nil)
	n := t.root
	for _, r := range s {
		c, ok := n.children[r]
		if !ok {
			if !create {
				return path, rs, false
			}
			if n.children == nil {
				n.children = map[rune]*node{}
			}
			c = &node{}
			n.children[r] = c
		}
		n = c
		path, rs = append(path, n), append(rs, r)
	}
	return path, rs, true
}

// Insert 新建终端串（频率 f）或对已存在串累加频率。
func (t *Trie) Insert(s string, f int) error {
	if err := CheckString(s); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	path, _, _ := t.walk(s, true)
	t.visited += len(path)
	n := path[len(path)-1]
	if n.terminal {
		n.freq += f
		return nil
	}
	n.terminal, n.freq = true, f
	for _, p := range path {
		p.refs++
	}
	t.count++
	return nil
}

// Delete 整条移除 s；沿路径引用计数 -1，归零节点自深向浅剪掉。
func (t *Trie) Delete(s string) error {
	if err := CheckString(s); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	path, rs, ok := t.walk(s, false)
	t.visited += len(path)
	if !ok || !path[len(path)-1].terminal {
		return ErrNotFound
	}
	path[len(path)-1].terminal = false
	t.count--
	for _, p := range path {
		p.refs--
	}
	for i := len(path) - 1; i >= 1 && path[i].refs == 0; i-- {
		delete(path[i-1].children, rs[i-1])
	}
	return nil
}

// Collect 返回以 prefix 开头的全部 (串, 频率)；prefix 无匹配时返回空。
func (t *Trie) Collect(prefix string) ([]Candidate, error) {
	if err := CheckString(prefix); err != nil {
		return nil, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	path, _, ok := t.walk(prefix, false)
	if !ok {
		return nil, nil
	}
	var out []Candidate
	var dfs func(*node, []rune)
	dfs = func(cur *node, buf []rune) {
		if cur.terminal {
			out = append(out, Candidate{Word: string(buf), Freq: cur.freq})
		}
		for r, c := range cur.children {
			dfs(c, append(buf, r))
		}
	}
	dfs(path[len(path)-1], []rune(prefix))
	return out, nil
}

func (t *Trie) Count() int { t.mu.RLock(); defer t.mu.RUnlock(); return t.count }
