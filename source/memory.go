package source

import "sync"

// Memory is a concurrency-safe mutable byte source held entirely in process
// memory. Its length may change at any time via SetBytes or Resize.
type Memory struct {
	mu  sync.RWMutex
	buf []byte
}

// NewMemory creates a Memory holding a copy of data.
func NewMemory(data []byte) *Memory {
	buf := make([]byte, len(data))
	copy(buf, data)
	return &Memory{buf: buf}
}

// SetBytes replaces the whole content with a copy of data.
func (m *Memory) SetBytes(data []byte) {
	buf := make([]byte, len(data))
	copy(buf, data)
	m.mu.Lock()
	m.buf = buf
	m.mu.Unlock()
}

// Resize grows (zero-filling) or truncates the content.
func (m *Memory) Resize(n int64) {
	m.mu.Lock()
	if n < int64(len(m.buf)) {
		m.buf = m.buf[:n]
	} else {
		grown := make([]byte, n)
		copy(grown, m.buf)
		m.buf = grown
	}
	m.mu.Unlock()
}

// Size returns the current content length.
func (m *Memory) Size() int64 {
	m.mu.RLock()
	n := int64(len(m.buf))
	m.mu.RUnlock()
	return n
}

// ReadAt implements Source. Reads starting past the end yield n==0 with no
// error; reads intersecting the end return the available suffix.
func (m *Memory) ReadAt(p []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if off >= int64(len(m.buf)) || off < 0 {
		return 0, nil
	}
	return copy(p, m.buf[off:]), nil
}
