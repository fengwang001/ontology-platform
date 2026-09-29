package raid5

import (
	"io"
	"sync"
)

// memJournalFile 是内存版日志存储，断电后内容保留（模拟落盘文件）。
type memJournalFile struct {
	mu  sync.Mutex
	buf []byte
	pos int64
}

func newMemJournalFile() *memJournalFile { return &memJournalFile{} }

func (m *memJournalFile) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := m.pos + int64(len(p))
	if int(end) > len(m.buf) {
		grown := make([]byte, end)
		copy(grown, m.buf[:m.pos])
		m.buf = grown
	}
	copy(m.buf[m.pos:end], p)
	m.pos = end
	return len(p), nil
}

func (m *memJournalFile) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pos >= int64(len(m.buf)) {
		return 0, io.EOF
	}
	n := copy(p, m.buf[m.pos:])
	if n == 0 {
		return 0, io.EOF
	}
	m.pos += int64(n)
	return n, nil
}

func (m *memJournalFile) Seek(offset int64, whence int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch whence {
	case 0:
		m.pos = offset
	case 1:
		m.pos += offset
	case 2:
		m.pos = int64(len(m.buf)) + offset
	}
	return m.pos, nil
}

func (m *memJournalFile) Truncate(size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size == 0 {
		m.buf = nil
		m.pos = 0
		return nil
	}
	if int(size) < len(m.buf) {
		m.buf = m.buf[:size]
	}
	return nil
}

func (m *memJournalFile) Sync() error  { return nil }
func (m *memJournalFile) Close() error { return nil }

// bytes 返回当前日志字节（测试判定用）。
func (m *memJournalFile) bytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.buf...)
}
