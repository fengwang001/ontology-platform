package allocator

import (
	"context"
	"sort"
	"sync"
)

// FaultFunc 是预留故障注入钩子：在 CAS 原子判定前调用，
// 返回 ReserveOK 表示不注入故障；返回 ReserveFailed / ReserveUnknown
// 分别模拟「明确未落盘」与「结果未知」。
type FaultFunc func(tenant string, old, next Watermark) ReserveStatus

type memRecord struct {
	n, b, v int
	wm      Watermark
}

// MemStore 是 Store 的并发安全内存实现，可用于测试与单机持久化（如嵌入进程）。
// 它通过条件写模拟 CAS：只有当前水位等于 old 时才推进到 next。
type MemStore struct {
	mu      sync.Mutex
	records map[string]memRecord
	fault   FaultFunc
}

// NewMemStore 创建内存持久化层。fault 可为 nil。
func NewMemStore(fault FaultFunc) *MemStore {
	return &MemStore{records: make(map[string]memRecord), fault: fault}
}

func (s *MemStore) RegisterTenant(_ context.Context, tenant string, n, b, v int, wm Watermark) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[tenant]; ok {
		return ErrTenantExists
	}
	s.records[tenant] = memRecord{n: n, b: b, v: v, wm: wm}
	return nil
}

func (s *MemStore) LoadTenant(_ context.Context, tenant string) (int, int, int, Watermark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[tenant]
	if !ok {
		return 0, 0, 0, Watermark{}, ErrTenantNotFound
	}
	return rec.n, rec.b, rec.v, rec.wm, nil
}

func (s *MemStore) ListTenants(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.records))
	for name := range s.records {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (s *MemStore) CompareAndSwapReserve(_ context.Context, tenant string, old, next Watermark) (ReserveStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[tenant]
	if !ok {
		// 租户不存在属于明确的程序性失败，持久化没有任何改变。
		return ReserveFailed, ErrTenantNotFound
	}
	if s.fault != nil {
		switch status := s.fault(tenant, old, next); status {
		case ReserveFailed:
			// 模拟明确失败：不检查、不写入。
			return ReserveFailed, nil
		case ReserveUnknown:
			// 模拟「写入结果未知」：按最坏情况，视为写入已生效。
			if rec.wm == old {
				rec.wm = next
				s.records[tenant] = rec
			}
			return ReserveUnknown, nil
		}
	}
	if rec.wm != old {
		// 与预期旧水位不符：说明状态已被先前的（可能未知的）预留推进，
		// 此时持久化并未因本次调用改变，属于明确未落盘。
		return ReserveFailed, nil
	}
	rec.wm = next
	s.records[tenant] = rec
	return ReserveOK, nil
}
