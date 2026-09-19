package ontology

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"sync"
)

// session 是一次遍历的快照状态。游标本身不推进；
// 会话只记录累计统计与失效状态，因此同一游标可幂等重复 Scan。
type session struct {
	mu       sync.Mutex
	keys     []string // 首次 Scan 时刻的排序主键快照
	frontier int      // 已确认处理到的快照下标（不含）
	inserts  int      // 遍历期间插入次数
	deletes  int      // 遍历期间删除次数
	discard  int      // 累计丢弃（快照元素被删除）数
	returned int      // 已返回的快照元素数
	valid    bool
}

func newSession(keys []string) *session {
	return &session{keys: keys, valid: true}
}

func (s *Store) registerSession(sn *session) (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	s.mu.Lock()
	s.sessions[id] = sn
	s.mu.Unlock()
	return id, nil
}

func (s *Store) lookupSession(id string) (*session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sn, ok := s.sessions[id]
	return sn, ok
}

// InvalidateSession 显式失效一个遍历会话。未知 ID 不报错。
func (s *Store) InvalidateSession(id string) {
	s.mu.Lock()
	sn, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
	if ok {
		sn.mu.Lock()
		sn.valid = false
		sn.mu.Unlock()
	}
}

// SessionID 返回该页所属的遍历会话 ID，用于失效或查询统计。
func (p Page) SessionID() string { return p.sessionID }

func (sn *session) changeKind() ChangeKind {
	var k ChangeKind
	if sn.inserts > 0 {
		k |= ChangeInsert
	}
	if sn.deletes > 0 {
		k |= ChangeDelete
	}
	return k
}

// Stats 返回会话的累计统计；未知/已失效会话返回 Valid=false。
func (s *Store) Stats(sessionID string) SessionStats {
	sn, ok := s.lookupSession(sessionID)
	if !ok {
		return SessionStats{}
	}
	sn.mu.Lock()
	defer sn.mu.Unlock()
	return SessionStats{
		Changes:       sn.changeKind(),
		Inserted:      sn.inserts,
		Deleted:       sn.deletes,
		Discarded:     sn.discard,
		SnapshotTotal: len(sn.keys),
		Returned:      sn.returned,
		Valid:         sn.valid,
	}
}

// Skipped 报告遍历累计跳过的元素数及原因分类。
func (s *Store) Skipped(sessionID string) SkippedReport {
	st := s.Stats(sessionID)
	if !st.Valid {
		return SkippedReport{}
	}
	return SkippedReport{
		Total:    st.Discarded,
		Reasons:  SkippedReason{DiscardedByDelete: st.Discarded},
		Valid:    true,
		Returned: st.Returned,
	}
}

// snapshotSortedKeys 导出快照键的拷贝（测试/调试用）。
func (sn *session) snapshotSortedKeys() []string {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	return append([]string(nil), sn.keys...)
}
