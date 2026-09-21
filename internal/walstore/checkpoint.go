package walstore

import (
	"io"
	"os"
)

// loadCheckpoint 加载检查点快照作为初始状态。
// checkpoint.dat 是一条与 WAL 同格式的记录；不存在则视为空状态。
// checkpoint.tmp 是写了一半的临时文件，直接忽略并删除。
func (s *Store) loadCheckpoint() error {
	// 上次检查点写到一半（rename 之前崩溃）：临时文件无意义。
	if err := os.Remove(s.ckptTmpPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.Open(s.ckptPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	batch, _, err := readRecord(f)
	if err != nil && err != io.EOF {
		// 检查点损坏（例如只写了一半）：不能信任它，但 WAL 仍完整，
		// 忽略检查点，回退到纯 WAL 恢复。
		return nil
	}
	for k, v := range batch {
		s.data[k] = v
	}
	return nil
}

// Checkpoint 把当前状态落为检查点，然后清空 WAL。
//
// 崩溃安全性来自顺序：先写临时快照 → fsync → rename → fsync 目录，
// 确认新检查点已就位之后才截断 WAL。任何中间点崩溃都不丢数据：
//   - rename 之前：旧检查点（若有）+ 完整 WAL；
//   - rename 之后、截断之前：新检查点 + 完整 WAL，回放幂等无副作用。
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}

	snapshot := make(map[string]string, len(s.data))
	for k, v := range s.data {
		snapshot[k] = v
	}
	rec := encodeRecord(snapshot)

	tmp, err := os.OpenFile(s.ckptTmpPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := tmp.Write(rec); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(s.ckptTmpPath(), s.ckptPath()); err != nil {
		return err
	}
	if err := fsyncDir(s.dir); err != nil {
		return err
	}

	// 新检查点已可靠就位，可以安全丢弃 WAL 历史。
	if err := s.wal.Truncate(0); err != nil {
		return err
	}
	if _, err := s.wal.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return s.wal.Sync()
}
