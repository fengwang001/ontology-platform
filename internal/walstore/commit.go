package walstore

// Commit 原子提交一批键值：先整体编码为一条 WAL 记录写入并
// fsync，成功后才应用到内存态。返回 nil 即表示该批已持久化，
// 之后任何崩溃恢复都必须能看到它。
func (s *Store) Commit(batch map[string]string) error {
	if len(batch) == 0 {
		return ErrEmptyBatch
	}
	for k := range batch {
		if k == "" {
			return ErrEmptyKey
		}
	}
	rec := encodeRecord(batch)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, err := s.wal.Write(rec); err != nil {
		return err
	}
	if err := s.wal.Sync(); err != nil {
		return err
	}
	// 持久化成功后才可见，保证"已确认即持久"。
	for k, v := range batch {
		s.data[k] = v
	}
	return nil
}

// Get 读取键值；第二个返回值区分"键不存在"与"值为空串"。
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Len 返回当前键的数量。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}
