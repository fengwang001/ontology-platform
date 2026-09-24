// Package store 用内存 map 模拟目标存储：按业务键唯一、写入幂等，
// 支持注入写入失败，并按批次维护在途/已提交状态。
package store

import (
	"errors"
	"sync"
)

var (
	// ErrInjected 由失败注入产生。
	ErrInjected = errors.New("store: injected write failure")
	// ErrConflict 唯一键冲突：键已属于其它批次或以不同值提交。
	ErrConflict = errors.New("store: unique key conflict")
)

// Entry 存储中的一条记录。
type Entry struct {
	BatchID   string
	Value     []byte
	Committed bool
}

// FailFn 注入故障：返回 true 时该条 Put 失败且不生效。
type FailFn func(batchID string, seq int, key string) bool

// Store 并发安全的内存目标存储。
type Store struct {
	mu        sync.Mutex
	data      map[string]Entry
	fail      FailFn
	access    int // 线性对账访问计数（含 Snapshot）
	emptyDone map[string]bool
}

// New 创建空存储。
func New() *Store {
	return &Store{data: make(map[string]Entry)}
}

// InjectFail 注入写入失败函数（nil 清除）。
func (s *Store) InjectFail(f FailFn) {
	s.mu.Lock()
	s.fail = f
	s.mu.Unlock()
}

// Put 幂等写入。同批同键同值重复写无害；跨批或异值占用返回 ErrConflict。
// 注入失败时返回 ErrInjected，存储不发生变化。
func (s *Store) Put(batchID string, seq int, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil && s.fail(batchID, seq, key) {
		return ErrInjected
	}
	if e, ok := s.data[key]; ok {
		if e.BatchID != batchID || !bytesEqual(e.Value, value) {
			return ErrConflict
		}
	}
	s.data[key] = Entry{BatchID: batchID, Value: append([]byte(nil), value...)}
	return nil
}

// Match 判定 (key,value) 已属于该批次（在途或已提交均可）。
func (s *Store) Match(batchID, key string, value []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	s.access++
	return ok && e.BatchID == batchID && bytesEqual(e.Value, value)
}

// Commit 将该批次全部在途记录翻转为已提交。
func (s *Store) Commit(batchID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.data {
		if e.BatchID == batchID {
			e.Committed = true
			s.data[k] = e
		}
	}
}

// IsCommitted 报告批次是否已有已提交记录（空批次需另用标记，见 CommittedIDs）。
func (s *Store) IsCommitted(batchID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.data {
		if e.BatchID == batchID && e.Committed {
			return true
		}
	}
	return false
}

// MarkEmpty 标记零记录批次为“已提交”（无记录可翻转）。
func (s *Store) MarkEmpty(batchID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emptyDone == nil {
		s.emptyDone = map[string]bool{}
	}
	s.emptyDone[batchID] = true
}

// EmptyCommitted 报告零记录批次是否已完成提交。
func (s *Store) EmptyCommitted(batchID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emptyDone[batchID]
}

// Snapshot 返回全量快照（计 1 次访问），供线性对账。
func (s *Store) Snapshot() map[string]Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access++
	out := make(map[string]Entry, len(s.data))
	for k, e := range s.data {
		e.Value = append([]byte(nil), e.Value...)
		out[k] = e
	}
	return out
}

// AccessCount 返回对账观察到的存储访问次数。
func (s *Store) AccessCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.access
}

// ResetAccess 清零访问计数。
func (s *Store) ResetAccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access = 0
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
