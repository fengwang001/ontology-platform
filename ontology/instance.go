package ontology

import "sync"

type InstanceID string

// Instance 是一个对象实例的当前字段取值快照。
type Instance struct {
	ID     InstanceID
	Fields map[string]Value
}

// InstanceStore 只持有当前仍然存活的实例。逻辑删除（Tombstone）或
// 迁移走（MigrateOut）的实例立即从存储中移除，之后任何扫描都不会触及它们。
type InstanceStore struct {
	mu      sync.Mutex
	live    map[InstanceID]map[string]Value
	scanned int64
}

// NewInstanceStore 创建空存储。
func NewInstanceStore() *InstanceStore {
	return &InstanceStore{live: make(map[InstanceID]map[string]Value)}
}

// Put 写入或覆盖一个存活实例（字段定义版本由 ObjectType 负责校验）。
func (s *InstanceStore) Put(id InstanceID, fields map[string]Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]Value, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	s.live[id] = cp
}

// Tombstone 逻辑删除：实例立即从存活集合移除，之后任何扫描都不会再触及它。
func (s *InstanceStore) Tombstone(id InstanceID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live[id]; !ok {
		return false
	}
	delete(s.live, id)
	return true
}

// MigrateOut 表示实例已迁移到其他对象类型：与逻辑删除一样立即移除。
func (s *InstanceStore) MigrateOut(id InstanceID) bool {
	return s.Tombstone(id)
}

// LiveCount 返回当前存活实例总数——这是任意一次字段变更扫描量的上界。
func (s *InstanceStore) LiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live)
}

// Scan 按快照顺序遍历存活实例；fn 返回 false 可提前终止。
// 每次回调（无论是否提前终止）恰好对应当前存活集合中的一个实例，
// 访问总量 <= LiveCount()，且与历史上写入/删除/迁移过的实例总数无关。
func (s *InstanceStore) Scan(fn func(Instance) bool) {
	s.mu.Lock()
	ids := make([]InstanceID, 0, len(s.live))
	for id := range s.live {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.mu.Lock()
		fields, ok := s.live[id]
		s.mu.Unlock()
		if !ok {
			continue // 扫描期间被删除：跳过，不计费
		}
		cp := make(map[string]Value, len(fields))
		for k, v := range fields {
			cp[k] = v
		}
		s.scanned++
		if !fn(Instance{ID: id, Fields: cp}) {
			return
		}
	}
}

// ScannedSinceReset 返回自上次 ResetScanCounter 以来 Scan 实际访问的
// 存活实例数，供测试验证扫描开销与历史总量无关。
func (s *InstanceStore) ScannedSinceReset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanned
}

func (s *InstanceStore) ResetScanCounter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanned = 0
}
