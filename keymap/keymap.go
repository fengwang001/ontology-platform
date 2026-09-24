// Package keymap 按折叠键存取记录，冲突时保留首次插入的原始键。
package keymap

import (
	"errors"
	"sort"
	"sync"
	"unicode/utf8"

	"ontology/fold"
)

// 三类可判定的拒绝原因互不相同；ErrCorrupt 为自检失败。
var (
	ErrEmptyKey       = errors.New("keymap: empty key")
	ErrKeyTooLong     = errors.New("keymap: key too long")
	ErrTooManyEntries = errors.New("keymap: too many entries")
	ErrCorrupt        = errors.New("keymap: self-check failed")
)

// Entry 是一条记录，Key 为首次插入的原始写法，Value 可被覆盖。
type Entry struct {
	Key, Value string
}

// Map 是按折叠键索引的查找表，并发安全。
type Map struct {
	mu         sync.RWMutex
	entries    map[string]Entry // 折叠键 -> 记录
	maxKeyLen  int              // 键长上限（rune 数）
	maxEntries int              // 表项数上限
}

// New 以给定键长上限与表项数上限建表。
func New(maxKeyLen, maxEntries int) *Map {
	return &Map{entries: make(map[string]Entry), maxKeyLen: maxKeyLen, maxEntries: maxEntries}
}

// Put 写入记录；先校验后写入，被拒时不留任何痕迹。
func (m *Map) Put(key, value string) error {
	if n := utf8.RuneCountInString(key); n == 0 {
		return ErrEmptyKey
	} else if n > m.maxKeyLen {
		return ErrKeyTooLong
	}
	f := fold.Fold(key)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[f]; ok {
		e.Value = value // 覆盖值，保留首次插入的原始键
		m.entries[f] = e
		return nil
	}
	if len(m.entries) >= m.maxEntries {
		return ErrTooManyEntries
	}
	m.entries[f] = Entry{Key: key, Value: value}
	return nil
}

// Get 按折叠键查记录；Fold 相等则命中同一条。
func (m *Map) Get(key string) (Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[fold.Fold(key)]
	return e, ok
}

// Keys 按折叠键的字典序返回首次插入的原始键。
func (m *Map) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	fs := make([]string, 0, len(m.entries))
	for f := range m.entries {
		fs = append(fs, f)
	}
	sort.Strings(fs)
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = m.entries[f].Key
	}
	return out
}

// SelfCheck 核验：折叠键与原始键折叠结果一致、无共享折叠键、计数一致。
func (m *Map) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for f, e := range m.entries {
		if f == "" || fold.Fold(e.Key) != f {
			return ErrCorrupt
		}
		n++
	}
	if n != len(m.entries) {
		return ErrCorrupt
	}
	return nil
}
