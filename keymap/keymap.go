package keymap

import (
	"errors"
	"sort"
	"sync"

	"ontology/fold"
)

var (
	ErrEmptyKey     = errors.New("empty key")
	ErrKeyTooLong   = errors.New("key length exceeds limit")
	ErrMapFull      = errors.New("map entry count exceeds limit")
	ErrInconsistent = errors.New("lookup map self-check failed")
)

type entry struct {
	original string
	value    any
}

type Map struct {
	folder   *fold.Folder
	records  map[string]entry
	maxKey   int
	maxItems int
	mu       sync.RWMutex
}

func New(maxKeyRunes, maxItems int) *Map {
	return &Map{
		folder:   fold.New(),
		records:  make(map[string]entry),
		maxKey:   maxKeyRunes,
		maxItems: maxItems,
	}
}

func (m *Map) Put(key string, value any) error {
	if key == "" {
		return ErrEmptyKey
	}
	if keyLength(key) > m.maxKey {
		return ErrKeyTooLong
	}
	folded := m.folder.Fold(key)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.records[folded]; !exists && len(m.records) >= m.maxItems {
		return ErrMapFull
	}
	current := m.records[folded]
	if current.original == "" {
		current.original = key
	}
	current.value = value
	m.records[folded] = current
	return nil
}

func (m *Map) Get(key string) (any, bool) {
	if key == "" || keyLength(key) > m.maxKey {
		return nil, false
	}
	folded := m.folder.Fold(key)
	m.mu.RLock()
	defer m.mu.RUnlock()
	record, ok := m.records[folded]
	return record.value, ok
}

func (m *Map) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.records))
	for folded, record := range m.records {
		if m.folder.Fold(record.original) != folded {
			return nil
		}
		keys = append(keys, record.original)
	}
	sort.Slice(keys, func(i, j int) bool {
		return m.folder.Fold(keys[i]) < m.folder.Fold(keys[j])
	})
	return keys
}

func (m *Map) Check() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]struct{}, len(m.records))
	for folded, record := range m.records {
		if m.folder.Fold(record.original) != folded {
			return ErrInconsistent
		}
		if _, duplicate := seen[folded]; duplicate {
			return ErrInconsistent
		}
		seen[folded] = struct{}{}
	}
	if len(seen) != len(m.records) {
		return ErrInconsistent
	}
	return nil
}

func keyLength(key string) int { return len([]rune(key)) }
