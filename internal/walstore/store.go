package walstore

import (
	"os"
	"path/filepath"
	"sync"
)

const (
	walFileName = "wal.log"
	chkFileName = "wal.log.chk"
	tmpFileName = "wal.log.chk.tmp"
)

// Store 是一个带预写日志的键值存储。Commit 先写 WAL 并 fsync，
// 再应用到内存态，因此返回 nil 即代表已持久化。
type Store struct {
	mu     sync.RWMutex
	data   map[string]string
	wal    *os.File
	dir    string
	closed bool
}

// Open 打开（必要时创建）dir 下的存储，并回放 wal.log 恢复状态。
// 日志尾部的不完整记录会被安全截断，永不因尾部垃圾而拒绝启动。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, walFileName),
		os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s := &Store{data: make(map[string]string), wal: f, dir: dir}
	if err := s.recover(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层文件。重复调用安全。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.wal.Close()
}
