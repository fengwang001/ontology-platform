package source

import (
	"context"
	"io"
	"sync"
)

// Memory is an in-process Source used by tests and callers that need faults.
type Memory struct {
	mu sync.Mutex

	data       []byte
	readLimit  int
	readError  error
	beforeRead func(*Memory)
	calls      int
}

func NewMemory(data []byte) *Memory {
	copied := append([]byte(nil), data...)
	return &Memory{data: copied}
}

func (m *Memory) Length(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.data)), nil
}

func (m *Memory) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	m.mu.Lock()
	if m.beforeRead != nil {
		hook := m.beforeRead
		m.mu.Unlock()
		hook(m)
		m.mu.Lock()
	}
	defer m.mu.Unlock()

	m.calls++
	if m.readError != nil {
		return 0, m.readError
	}
	if off < 0 || off > int64(len(m.data)) {
		return 0, ErrShortEOF
	}
	if off == int64(len(m.data)) || len(p) == 0 {
		return 0, io.EOF
	}

	available := copy(p, m.data[off:])
	if m.readLimit > 0 && available > m.readLimit {
		available = m.readLimit
	}
	if available < len(p) {
		return available, nil
	}
	return available, nil
}

func (m *Memory) SetReadLimit(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLimit = n
}

func (m *Memory) SetReadError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readError = err
}

func (m *Memory) SetBeforeRead(hook func(*Memory)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.beforeRead = hook
}

func (m *Memory) Replace(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append([]byte(nil), data...)
}

func (m *Memory) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}
