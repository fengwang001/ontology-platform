package ontology

import "sync"

type rwMutex struct {
	mu sync.RWMutex
}

func (m *rwMutex) lock() {
	m.mu.Lock()
}

func (m *rwMutex) unlock() {
	m.mu.Unlock()
}

func (m *rwMutex) rLock() {
	m.mu.RLock()
}

func (m *rwMutex) rUnlock() {
	m.mu.RUnlock()
}
