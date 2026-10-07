package lifecycle

import (
	"sort"
	"sync"
)

// storeEntry 是单个实例的互斥锁。
//
// 不变量：
//   - 任何修改实例内部字段（State/Attrs/Clock/out）的提交路径，
//     必须持有该实例（属主）的锁。
//   - 规划快照需要读取实例内部字段时，对“涉及集合”中的实例持有其锁；
//     其余实例只通过原子指针读取版本（级联扩展时再对它们加锁）。
//   - 加锁永远按 ID 字典序，杜绝加锁环；实例集合不相交的两个批
//     不共享任何锁，因此完全并行。
type storeEntry struct {
	mu sync.Mutex
}

// Snapshot 返回当前全部实例的浅快照（内部字段指针为只读约定）。
func (s *Store) Snapshot() *snap {
	return s.snapshot()
}

// AddInstance 放入一个实例（初始化辅助方法，串行使用即可）。
func (s *Store) AddInstance(inst *Instance) {
	cp := *inst
	if cp.Attrs == nil {
		cp.Attrs = map[string]AttrValue{}
	}
	if cp.out == nil {
		cp.out = map[string]map[string]bool{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := &atomicPtr{}
	slot.store(&cp)
	s.inst[cp.ID] = slot
	if _, ok := s.entry[cp.ID]; !ok {
		s.entry[cp.ID] = &storeEntry{}
	}
}

// GetInstance 返回实例当前版本的深拷贝（公共只读 API）。
func (s *Store) GetInstance(id string) *Instance {
	s.mu.RLock()
	slot, ok := s.inst[id]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	s.lockIDs([]string{id})
	defer s.unlockIDs([]string{id})
	v := slot.load()
	c := *v
	c.Attrs = cloneAttrs(v.Attrs)
	c.out = cloneOut(v.out)
	return &c
}

// HasLink 报告链接是否存在。
func (s *Store) HasLink(l Link) bool {
	s.mu.RLock()
	slot := s.inst[l.FromID]
	s.mu.RUnlock()
	if slot == nil {
		return false
	}
	s.lockIDs([]string{l.FromID})
	defer s.unlockIDs([]string{l.FromID})
	v := slot.load()
	if set := v.out[l.Type]; set != nil {
		return set[l.ToID]
	}
	return false
}

// LinksOf 返回实例经 linkType 出向连接的全部对端 ID。
func (s *Store) LinksOf(id, linkType string) []string {
	s.mu.RLock()
	slot := s.inst[id]
	s.mu.RUnlock()
	if slot == nil {
		return nil
	}
	s.lockIDs([]string{id})
	defer s.unlockIDs([]string{id})
	v := slot.load()
	set := v.out[linkType]
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Clock 返回当前逻辑时钟值（全局）。
func (s *Store) Clock() int64 {
	return s.clock.Load()
}

// lockIDs/unlockIDs 按 ID 字典序加/放实例锁。调用方不得已持有 s.mu。
func (s *Store) lockIDs(ids []string) {
	uniq := uniqueSorted(ids)
	s.mu.RLock()
	locked := make([]*storeEntry, 0, len(uniq))
	for _, id := range uniq {
		e, ok := s.entry[id]
		if !ok {
			// 未知实例的锁仍需登记，保证后续并发 AddInstance 时顺序一致。
			e = &storeEntry{}
			s.entry[id] = e
		}
		e.mu.Lock()
		locked = append(locked, e)
	}
	s.mu.RUnlock()
	_ = locked
}

func (s *Store) unlockIDs(ids []string) {
	uniq := uniqueSorted(ids)
	locked := make([]*storeEntry, 0, len(uniq))
	for _, id := range uniq {
		if e, ok := s.entry[id]; ok {
			locked = append(locked, e)
		} else {
			locked = append(locked, nil)
		}
	}
	for i := len(locked) - 1; i >= 0; i-- {
		if locked[i] != nil {
			locked[i].mu.Unlock()
		}
	}
}

func cloneOut(m map[string]map[string]bool) map[string]map[string]bool {
	c := make(map[string]map[string]bool, len(m))
	for typ, set := range m {
		s2 := make(map[string]bool, len(set))
		for k, v := range set {
			s2[k] = v
		}
		c[typ] = s2
	}
	return c
}

func uniqueSorted(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	w := 0
	for i := range out {
		if i > 0 && out[i] == out[i-1] {
			continue
		}
		out[w] = out[i]
		w++
	}
	return out[:w]
}
