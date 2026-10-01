package backup

// Session 是指向某备份还原结果的只读会话。会话未关闭前，
// 目标备份及其祖先均不允许删除，因此会话期间其回溯路径上的
// 块内容始终稳定，读取结果与打开时一致。
type Session struct {
	m      *Manager
	target *bk
	closed bool
}

// BackupID 返回该会话指向的备份 ID。
func (s *Session) BackupID() string {
	return s.target.id
}

// ReadBlock 读取还原结果中第 index 块的内容（返回一份拷贝）。
// 每块取从目标备份回溯到链首遇到的第一份内容。
func (s *Session) ReadBlock(index int) ([]byte, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.closed {
		s.m.log.Printf("Restore.ReadBlock 输入: id=%s index=%d -> 输出: 拒绝(%v), 判定依据: 会话已关闭",
			s.target.id, index, ErrSessionClosed)
		return nil, ErrSessionClosed
	}
	if index < 0 || index >= s.m.blocks {
		s.m.log.Printf("Restore.ReadBlock 输入: id=%s index=%d -> 输出: 拒绝, 判定依据: 块号越界",
			s.target.id, index)
		return nil, ErrBlockIndexOutOfRange
	}
	data := s.m.resolveLocked(s.target, index)
	out := append([]byte(nil), data...)
	s.m.log.Printf("Restore.ReadBlock 输入: id=%s index=%d -> 输出: %d 字节, 判定依据: 沿父链回溯命中第一份内容",
		s.target.id, index, len(out))
	return out, nil
}

// Close 关闭会话，释放其对回溯路径上备份的删除保护。
// 重复关闭是安全的空操作。
func (s *Session) Close() {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(s.m.sessions, s)
	s.m.log.Printf("Restore.Close 输入: id=%s -> 输出: 成功, 判定依据: 会话注销, 释放回溯路径删除保护",
		s.target.id)
}
