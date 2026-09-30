package keystream

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Fault 描述针对持久化操作的一次注入故障。
type Fault struct {
	// Op 目标操作： "save_reserved"、"create_tenant"。
	Op string
	// Call 该操作的第几次调用（从 1 起）被命中；0 表示每次都命中。
	Call int
	// Unknown true 表示结果未知（可能已落盘），false 表示明确失败。
	Unknown bool
}

// ScriptedStore 是内存持久层，按故障脚本返回失败，用于确定性测试。
type ScriptedStore struct {
	mu      sync.Mutex
	faults  []Fault
	calls   map[string]int
	records map[string]TenantRecord
}

// NewScriptedStore 创建空的脚本化内存持久层。
func NewScriptedStore(faults ...Fault) *ScriptedStore {
	return &ScriptedStore{
		faults:  append([]Fault(nil), faults...),
		calls:   make(map[string]int),
		records: make(map[string]TenantRecord),
	}
}

func (s *ScriptedStore) CreateTenant(ctx context.Context, tenant string, rec TenantRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.matchFaultLocked("create_tenant"); err != nil {
		return err
	}
	if _, exists := s.records[tenant]; exists {
		return ErrStoreConflict
	}
	rec.Reserved = 0
	s.records[tenant] = rec
	return nil
}

func (s *ScriptedStore) GetTenant(ctx context.Context, tenant string) (TenantRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[tenant]
	return rec, ok, nil
}

func (s *ScriptedStore) SaveReserved(ctx context.Context, tenant string, reserved int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[tenant]
	if !ok {
		return &PersistError{Op: "save_reserved", Unknown: false, Err: ErrTenantNotFound}
	}

	if err := s.matchFaultLocked("save_reserved"); err != nil {
		// Unknown 故障模拟“可能已落盘”：内存（扮演真实磁盘）推进高水位。
		if pe, _ := err.(*PersistError); pe != nil && pe.Unknown {
			rec.Reserved = reserved
			s.records[tenant] = rec
		}
		return err
	}

	rec.Reserved = reserved
	s.records[tenant] = rec
	return nil
}

func (s *ScriptedStore) ListTenants(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.records))
	for name := range s.records {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// SetFaults 替换故障脚本（供测试在运行过程中编排故障）。
func (s *ScriptedStore) SetFaults(faults ...Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append([]Fault(nil), faults...)
}

// CallCount 返回某操作已发生的调用次数。
func (s *ScriptedStore) CallCount(op string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[op]
}

// Reserved 返回某租户持久层中的高水位（测试断言用）。
func (s *ScriptedStore) Reserved(tenant string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[tenant].Reserved
}

func (s *ScriptedStore) matchFaultLocked(op string) error {
	call := s.calls[op] + 1
	s.calls[op] = call
	for _, f := range s.faults {
		if f.Op != op {
			continue
		}
		if f.Call != 0 && f.Call != call {
			continue
		}
		return &PersistError{
			Op:      op,
			Unknown: f.Unknown,
			Err:     fmt.Errorf("injected fault on %s call %d", op, call),
		}
	}
	return nil
}
