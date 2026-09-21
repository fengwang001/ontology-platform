package walstore

import (
	"os"
	"sync"
)

// Store 是基于 WAL 的崩溃安全键值存储。
//
// 持久化模型：每次 Commit 把整批编码成一条带 CRC 的原子帧，
// 一次性 WriteAt/Write 追加并 fsync；恢复时只有“完整且 CRC 正确”
// 的帧才生效，因此一个批次要么整体可见，要么整体不可见。
type Store struct {
	mu    sync.Mutex // 串行化提交与检查点，保证提交顺序确定
	f     *os.File
	dir   string
	state map[string]string
	closed bool
}

// walName 是目录下固定使用的 WAL 文件名。
const walName = "wal.log"

// Open 打开（或创建）dir 下的存储并重放日志恢复状态。
// 任何尾部损坏都必须被裁掉，Open 永不因半截记录失败。
func Open(dir string) (*Store, error) {
	f, state, err := recoverLog(dir)
	if err != nil {
		return nil, err
	}
	return &Store{f: f, dir: dir, state: state}, nil
}

// validateBatch 检查空批次与空键。
func validateBatch(batch map[string]string) error {
	if len(batch) == 0 {
		return ErrEmptyBatch
	}
	for k := range batch {
		if k == "" {
			return ErrEmptyKey
		}
	}
	return nil
}

// Commit 原子提交一批键值：fsync 成功返回后即持久，
// 之后任何崩溃/截断（不越过本批写入起点）都必须完整可见。
func (s *Store) Commit(batch map[string]string) error {
	if err := validateBatch(batch); err != nil {
		return err
	}
	frame := encodeFrame(recBatch, encodeBatch(batch))

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	// 单帧一次 Write + Fsync：要么整帧落盘，要么不存在，
	// 不会出现跨批次交错（mu 也保证没有并发写）。
	if _, err := s.f.Write(frame); err != nil {
		return err
	}
	if err := s.f.Sync(); err != nil {
		return err
	}
	for k, v := range batch {
		s.state[k] = v
	}
	return nil
}

// Get 查询键，value=="" 与不存在通过 ok 区分。
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.state[key]
	return v, ok
}

// Len 返回当前键的数量。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.state)
}

// Close 关闭底层文件。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.f.Close()
}
