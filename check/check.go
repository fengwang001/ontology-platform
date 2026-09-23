// Package check 提供独立于 sem 内部账本的影子模型，用于并发逐步比对。
package check

import (
	"sync"

	"ontology/sem"
)

// Shadow 用自己的计数器复算占用：成功 Acquire/TryAcquire 加，成功 Release 减。
type Shadow struct {
	mu   sync.RWMutex
	used int64
	cap  int64
}

// New 创建与信号量同容量的影子模型。
func New(cap int64) *Shadow { return &Shadow{cap: cap} }

// Begin 在一次可能改动占用的操作前持有读锁；与 Verify 的写锁串行，
// 保证「逐步比对」时没有任何操作只做了一半。
func (m *Shadow) Begin() { m.mu.RLock() }

// End 释放 Begin 获取的读锁。
func (m *Shadow) End() { m.mu.RUnlock() }

// Granted 记录一次成功的额度获取。
func (m *Shadow) Granted(n int64) {
	m.mu.Lock()
	m.used += n
	m.mu.Unlock()
}

// Returned 记录一次成功的额度归还。
func (m *Shadow) Returned(n int64) {
	m.mu.Lock()
	m.used -= n
	m.mu.Unlock()
}

// Used 返回影子模型当前占用。
func (m *Shadow) Used() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.used
}

// Verify 比对影子占用、信号量占用与不变量 0<=used<=Cap，全部成立才返回 nil。
func (m *Shadow) Verify(s *sem.Sem) (bool, string) {
	m.mu.Lock()
	shadow := m.used
	m.mu.Unlock()
	st := s.Stats()
	switch {
	case !s.Healthy():
		return false, "sem ledger unhealthy"
	case shadow != st.Used:
		return false, "shadow/sem used diverged"
	case st.Used < 0 || st.Used > st.Cap:
		return false, "used out of [0,Cap]"
	}
	return true, ""
}
