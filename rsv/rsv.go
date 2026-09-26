// Package rsv 预留管理：id 分配、Release 校验。依赖 res，不依赖 api。
package rsv

import (
	"errors"
	"sync"

	"ontology/res"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrNeed  = errors.New("rsv: need out of range")
	ErrRange = errors.New("rsv: end must be greater than start")
	ErrNoID  = errors.New("rsv: no such active reservation id")
)

// Mgr 在 res.Core 之上管理 id 与已激活预留。所有方法可并发调用。
type Mgr struct {
	mu     sync.Mutex
	core   *res.Core
	next   int64
	active map[int64]res.Interval
}

func NewMgr(capacity int64) *Mgr {
	return &Mgr{core: res.NewCore(capacity), next: 1, active: map[int64]res.Interval{}}
}

func (m *Mgr) Capacity() int64 { return m.core.Capacity() }

// Reserve 校验前置条件（失败不改任何状态），再按峰值判定接受或拒绝。
func (m *Mgr) Reserve(start, end, need int64) (id int64, ok bool, err error) {
	if need < 1 || need > m.core.Capacity() {
		return 0, false, ErrNeed
	}
	if end <= start {
		return 0, false, ErrRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.core.CanAdd(start, end, need) {
		return 0, false, nil // 容量不足：拒绝，不留痕（也不消耗 id）
	}
	id = m.next
	m.next++
	m.core.Add(start, end, need)
	m.active[id] = res.Interval{Start: start, End: end, Need: need}
	return id, true, nil
}

// Release 释放已激活预留；id 不存在或已释放返回 ErrNoID。
func (m *Mgr) Release(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	iv, ok := m.active[id]
	if !ok {
		return ErrNoID
	}
	delete(m.active, id)
	m.core.Remove(iv.Start, iv.End, iv.Need)
	return nil
}

// Active 返回当前已激活预留条数。
func (m *Mgr) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}
