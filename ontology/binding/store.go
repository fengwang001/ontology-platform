package binding

import "sync"

// Instance 是一条链接实例在绑定维度上的投影。
type Instance struct {
	ID       string
	LinkType string
	// Deleted 为 true 表示该实例已不再存活。
	Deleted bool
}

// InstanceStore 承载链接实例。核验时需要遍历的实例数量不得超过
// 使用该绑定依据的链接实例当前存活总数。
type InstanceStore interface {
	// LiveCount 返回指定链接类型当前存活的实例数。
	LiveCount(linkType string) int
	// VisitLive 按当前存活集合逐个访问，访问期间发生的删除由实现决定
	// 是否可见；访问总数不得超过调用开始时的存活总数。
	VisitLive(linkType string, fn func(Instance) error) error
}

type MemoryInstanceStore struct {
	mu        sync.Mutex
	instances map[string]Instance
	// visits 统计每个链接类型累计被遍历的实例数，供测试验证开销上限。
	visits map[string]int
}

func NewMemoryInstanceStore() *MemoryInstanceStore {
	return &MemoryInstanceStore{
		instances: map[string]Instance{},
		visits:    map[string]int{},
	}
}

func (s *MemoryInstanceStore) Add(inst Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst.Deleted = false
	s.instances[inst.ID] = inst
}

func (s *MemoryInstanceStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[id]; ok {
		inst.Deleted = true
		s.instances[id] = inst
	}
}

func (s *MemoryInstanceStore) LiveCount(linkType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, inst := range s.instances {
		if inst.LinkType == linkType && !inst.Deleted {
			n++
		}
	}
	return n
}

// Visits 返回某链接类型自创建以来在核验中被遍历的实例总数。
func (s *MemoryInstanceStore) Visits(linkType string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visits[linkType]
}

func (s *MemoryInstanceStore) VisitLive(linkType string, fn func(Instance) error) error {
	// 先在锁内快照当前存活集合，随后无锁遍历。
	// 快照大小 == 本次调用开始时刻的存活总数，遍历数不可能超过它；
	// 对象类型历史上已删除的实例自始至终不在快照内。
	s.mu.Lock()
	snapshot := make([]Instance, 0)
	for _, inst := range s.instances {
		if inst.LinkType == linkType && !inst.Deleted {
			snapshot = append(snapshot, inst)
		}
	}
	n := len(snapshot)
	s.visits[linkType] += n
	s.mu.Unlock()

	for _, inst := range snapshot {
		if err := fn(inst); err != nil {
			return err
		}
	}
	return nil
}
