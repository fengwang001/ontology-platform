// Package keymap 按折叠键存取字符串记录，冲突时保留首次插入的原始键。
package keymap

import (
	"errors"
	"sort"
	"sync"
	"unicode/utf8"

	"ontology/fold"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyKey = errors.New("keymap: empty key")
	ErrKeyLong  = errors.New("keymap: key too long")
	ErrFull     = errors.New("keymap: too many entries")
)

type entry struct{ orig, val string }

// Map 是并发安全的折叠键查找表。
type Map struct {
	mu     sync.RWMutex
	maxKey int // 键长上限（rune 数）
	maxN   int // 表项数上限
	m      map[string]entry
}

// New 建表；maxKey 为键长上限（rune 数），maxN 为表项数上限。
func New(maxKey, maxN int) *Map {
	return &Map{maxKey: maxKey, maxN: maxN, m: make(map[string]entry)}
}

// Put 写入记录；同一折叠键重复 Put 覆盖值但保留首次的原始键；校验失败不改变表内容。
func (t *Map) Put(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if utf8.RuneCountInString(key) > t.maxKey {
		return ErrKeyLong
	}
	fk := fold.String(key)
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[fk]
	if !ok {
		if len(t.m) >= t.maxN {
			return ErrFull
		}
		e.orig = key
	}
	e.val = val
	t.m[fk] = e
	return nil
}

// Get 按折叠键命中记录。
func (t *Map) Get(key string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.m[fold.String(key)]
	return e.val, ok
}

// Keys 按折叠键字典序返回首次插入的原始键。
func (t *Map) Keys() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	fks := make([]string, 0, len(t.m))
	for fk := range t.m {
		fks = append(fks, fk)
	}
	sort.Strings(fks)
	out := make([]string, len(fks))
	for i, fk := range fks {
		out[i] = t.m[fk].orig
	}
	return out
}

// SelfCheck 核验：每项折叠键==fold(原始键)、无两项共享折叠键、计数一致。
func (t *Map) SelfCheck() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := make(map[string]struct{}, len(t.m))
	for fk, e := range t.m {
		if fold.String(e.orig) != fk {
			return errors.New("keymap: folded key mismatch")
		}
		if _, dup := seen[fk]; dup {
			return errors.New("keymap: duplicate folded key")
		}
		seen[fk] = struct{}{}
	}
	if len(seen) != len(t.m) {
		return errors.New("keymap: entry count mismatch")
	}
	return nil
}
