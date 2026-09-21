package walstore

import (
	"os"
	"path/filepath"
)

// Checkpoint 把当前状态整体写入 wal.log.chk（先写临时文件、
// fsync、再原子 rename），成功后清空 wal.log。任何时刻崩溃都
// 安全：rename 之前崩溃，旧检查点与完整 WAL 都在；rename 之后
// 崩溃，新检查点完整，WAL 中剩余记录回放也是幂等的。
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}

	snap := make(map[string]string, len(s.data))
	for k, v := range s.data {
		snap[k] = v
	}

	tmp := filepath.Join(s.dir, tmpFileName)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(encodeRecord(snap)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, chkFileName)); err != nil {
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}

	// 检查点已持久化，WAL 中之前的记录均可丢弃。
	if err := s.wal.Truncate(0); err != nil {
		return err
	}
	if _, err := s.wal.Seek(0, 0); err != nil {
		return err
	}
	if err := s.wal.Sync(); err != nil {
		return err
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	// 原实现漏掉了 Close：每次 syncDir 泄漏一个目录 fd，
	// Checkpoint 每轮调用两次，长期运行必然耗尽 fd 上限。
	defer d.Close()
	return d.Sync()
}
