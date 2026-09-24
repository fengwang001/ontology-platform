package store

import (
	"sort"
	"sync"
)

type Retainer interface {
	Version() uint64
	Retain(key string, old []byte, exists bool)
}

type Registry interface {
	Add(Retainer)
	Remove(Retainer)
	OldestVersion() (uint64, bool)
	Retain(key string, old []byte, exists bool)
}

type SnapshotStore interface {
	Keys(retain map[string][]byte) []string
	Get(key string, retain map[string][]byte) ([]byte, bool)
	BeginRead()
	EndRead()
}

type Store struct {
	mu       sync.RWMutex
	data     map[string][]byte
	version  uint64
	registry Registry
	reads    uint64
	readWG   sync.WaitGroup
}

func New(registry Registry) *Store {
	return &Store{data: make(map[string][]byte), registry: registry}
}

func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func (s *Store) Reads() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reads
}

func (s *Store) Write(key string, value []byte) uint64 {
	old, exists := s.lockedGetForWrite(key)

	if s.registry != nil {
		if oldest, ok := s.registry.OldestVersion(); ok && s.version+1 > oldest {
			s.registry.Retain(key, old, exists)
		}
	}

	copied := append([]byte(nil), value...)
	s.data[key] = copied
	s.version++

	s.mu.Unlock()
	return s.version
}

func (s *Store) Delete(key string) uint64 {
	old, exists := s.lockedGetForWrite(key)

	if s.registry != nil {
		if oldest, ok := s.registry.OldestVersion(); ok && s.version+1 > oldest {
			s.registry.Retain(key, old, exists)
		}
	}

	delete(s.data, key)
	s.version++

	s.mu.Unlock()
	return s.version
}

func (s *Store) lockedGetForWrite(key string) ([]byte, bool) {
	s.mu.Lock()
	old, exists := s.data[key]
	if exists {
		old = append([]byte(nil), old...)
	}
	return old, exists
}

func (s *Store) BeginRead() {
	s.readWG.Add(1)
}

func (s *Store) EndRead() {
	s.readWG.Done()
}

func (s *Store) WaitReads() {
	s.readWG.Wait()
}

func (s *Store) Keys(retain map[string][]byte) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]struct{}, len(s.data)+len(retain))
	keys := make([]string, 0, len(s.data)+len(retain))
	for key := range s.data {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range retain {
		if _, ok := seen[key]; ok; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *Store) Get(key string, retain map[string][]byte) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.reads++

	if value, ok := retain[key]; ok {
		if value == nil {
			return nil, false
		}
		return append([]byte(nil), value...), true
	}
	value, ok := s.data[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), value...), true
}
