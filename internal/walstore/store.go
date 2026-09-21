package walstore

import (
	"os"
	"path/filepath"
	"sync"
)

const (
	walFileName  = "wal.log"
	ckptFileName = "checkpoint.dat"
	ckptTmpName  = "checkpoint.tmp"
)

// Store 是一个带预写日志、可从崩溃中恢复的键值存储。
// 所有已确认（Commit 返回 nil）的批次在崩溃恢复后必须可见。
type Store struct {
	dir string

	mu     sync.RWMutex // 保护 data 与 wal 的写入串行化
	data   map[string]string
	wal    *os.File // 追加写的日志文件
	closed bool
}

// Open 打开（必要时创建）dir 下的存储，并执行崩溃恢复。
// wal.log 末尾的不完整记录会被安全丢弃并截断，保证后续可继续追加。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, data: make(map[string]string)}
	if err := s.loadCheckpoint(); err != nil {
		return nil, err
	}
	if err := s.replayWAL(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get 返回键对应的值；第二个返回值区分"不存在"与"值为空串"。
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Len 返回当前可见键的数量。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// Close 同步并关闭底层文件。关闭后再调用 Commit 会返回 ErrClosed。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.wal == nil {
		return nil
	}
	if err := s.wal.Sync(); err != nil {
		s.wal.Close()
		return err
	}
	return s.wal.Close()
}

// fsyncDir 刷目录项，保证 rename/创建在崩溃后仍然可见。
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Store) walPath() string     { return filepath.Join(s.dir, walFileName) }
func (s *Store) ckptPath() string    { return filepath.Join(s.dir, ckptFileName) }
func (s *Store) ckptTmpPath() string { return filepath.Join(s.dir, ckptTmpName) }
