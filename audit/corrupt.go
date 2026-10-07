package audit

// 本文件提供一组 Unsafe* 钩子，用于在测试与故障演练中模拟
// 底层存储被篡改/损坏（磁盘位翻转、恶意写入等）的情形。
// 生产代码不应调用这些方法；它们不更新任何哈希，因此必然
// 被完整性校验检测为破坏。

// UnsafeCorruptVersionContent 原地篡改某个规则版本的内容（不更新哈希）。
func (s *System) UnsafeCorruptVersionContent(id VersionID, mutate func(*RuleSet)) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	v, ok := s.st.getVersion(id)
	if !ok {
		return ErrVersionNotFound
	}
	mutate(&v.Content)
	return nil
}

// UnsafeCorruptRecordResult 原地篡改某条审计记录的判定结果（不更新哈希）。
func (s *System) UnsafeCorruptRecordResult(id RecordID, result Decision) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	r, ok := s.st.getRecord(id)
	if !ok {
		return ErrRecordNotFound
	}
	r.Result = result
	return nil
}

// UnsafeCorruptRecordVersionRef 原地篡改某条审计记录登记的版本标识。
func (s *System) UnsafeCorruptRecordVersionRef(id RecordID, versionID VersionID) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	r, ok := s.st.getRecord(id)
	if !ok {
		return ErrRecordNotFound
	}
	r.VersionID = versionID
	return nil
}

// UnsafeCorruptRecordHash 原地篡改某条审计记录的链哈希。
func (s *System) UnsafeCorruptRecordHash(id RecordID, chainHash string) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	r, ok := s.st.getRecord(id)
	if !ok {
		return ErrRecordNotFound
	}
	r.ChainHash = chainHash
	return nil
}

// UnsafeDeleteVersion 模拟版本条目被非法删除（绕过追加式约束）。
func (s *System) UnsafeDeleteVersion(id VersionID) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	delete(s.st.versions, id)
}
