package walstore

// Commit 原子提交一批键值：要么全部可见，要么全部不可见。
// 返回 nil 即表示该批次已 fsync 落盘，此后任意崩溃恢复都可见。
//
// 全程持有写锁，保证并发批次的"写日志 + fsync + 应用到内存"
// 作为一个串行序列完成：每个批次在文件中是一条完整记录，
// 崩溃恢复时只能看到整批或整批缺失，不可能只看到一部分。
func (s *Store) Commit(batch map[string]string) error {
	if err := validateBatch(batch); err != nil {
		return err
	}
	rec := encodeRecord(batch)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}

	// 记录先完整追加并 fsync，确认持久后才更新内存可见状态。
	if _, err := s.wal.Write(rec); err != nil {
		return err
	}
	if err := s.wal.Sync(); err != nil {
		return err
	}
	for k, v := range batch {
		s.data[k] = v
	}
	return nil
}
