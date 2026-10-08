package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// linkKey 是基数联合校验索引的键：某源实例上某链接类型的全部出边。
type linkKey struct {
	sourceRID   string
	linkTypeRID string
}

// Stats 是用于复杂度证明的确定性计数器。
type Stats struct {
	// AdjacencyScans 统计“遍历某实例全部已有链接”的次数。
	// 批量导入路径必须保持为 0，以此证明联合校验开销不随已有链接总数增长。
	AdjacencyScans int64
	// IndexOps 统计对基数索引的哈希访问次数，应只随本批次条目数线性增长。
	IndexOps int64
}

// Hooks 是测试用的故障注入与观察钩子。
type Hooks struct {
	// AfterLand 在单个条目落地后触发。
	// Atomic 模式下在持有存储写锁期间触发（读者仍被阻塞）；
	// BestEffort 模式下在释放写锁之后触发（读者可观察到中间态）。
	// 注意：钩子实现不得再调用 Store 的方法，否则会自死锁。
	AfterLand func(rid string)
	// UndoError 非nil且返回非nil错误时，模拟该实例逆操作（删除）失败。
	UndoError func(rid string) error
}

// Store 是对象图的内存储存，带基数联合校验所需的常量时间索引。
type Store struct {
	mu          sync.RWMutex
	objectTypes map[string]*ObjectType
	linkTypes   map[string]*LinkType
	objects     map[string]*ObjectInstance
	links       map[string]*LinkInstance
	// linkIndex 维护 (源实例, 链接类型) -> 目标集合，
	// 使“该源是否已有该类型链接”的判定为 O(1) 哈希访问，
	// 无需遍历源实例的全部已有链接。
	linkIndex map[linkKey]map[string]struct{}
	stats     Stats
	hooks     Hooks
}

func NewStore() *Store {
	return &Store{
		objectTypes: make(map[string]*ObjectType),
		linkTypes:   make(map[string]*LinkType),
		objects:     make(map[string]*ObjectInstance),
		links:       make(map[string]*LinkInstance),
		linkIndex:   make(map[linkKey]map[string]struct{}),
	}
}

// SetHooks 安装故障注入钩子，应在导入开始前调用。
func (s *Store) SetHooks(h Hooks) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = h
}

// Stats 返回计数器快照。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// ResetStats 清零计数器，便于测试按阶段断言。
func (s *Store) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = Stats{}
}

func (s *Store) RegisterObjectType(ot ObjectType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ot.RID == "" {
		return errors.New("对象类型 RID 不能为空")
	}
	if _, ok := s.objectTypes[ot.RID]; ok {
		return fmt.Errorf("对象类型 %s 已注册", ot.RID)
	}
	cp := ot
	s.objectTypes[ot.RID] = &cp
	return nil
}

func (s *Store) RegisterLinkType(lt LinkType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lt.RID == "" {
		return errors.New("链接类型 RID 不能为空")
	}
	if _, ok := s.linkTypes[lt.RID]; ok {
		return fmt.Errorf("链接类型 %s 已注册", lt.RID)
	}
	if lt.MaxTargetsPerSource < 0 {
		return errors.New("MaxTargetsPerSource 不能为负")
	}
	cp := lt
	s.linkTypes[lt.RID] = &cp
	return nil
}

// SeedObject 直接放入一个已存在的对象（测试夹具或批次外数据）。
func (s *Store) SeedObject(obj ObjectInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectTypes[obj.TypeRID]; !ok {
		return fmt.Errorf("未知对象类型 %s", obj.TypeRID)
	}
	if _, ok := s.objects[obj.RID]; ok {
		return fmt.Errorf("对象 %s 已存在", obj.RID)
	}
	cp := obj
	s.objects[obj.RID] = &cp
	return nil
}

// SeedLink 直接放入一条已存在的链接，并维护基数索引与基数约束。
func (s *Store) SeedLink(lnk LinkInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTypes[lnk.TypeRID]
	if !ok {
		return fmt.Errorf("未知链接类型 %s", lnk.TypeRID)
	}
	if _, ok := s.links[lnk.RID]; ok {
		return fmt.Errorf("链接 %s 已存在", lnk.RID)
	}
	if lt.MaxTargetsPerSource > 0 &&
		s.linkCountLocked(lnk.SourceRID, lnk.TypeRID)+1 > lt.MaxTargetsPerSource {
		return fmt.Errorf("链接 %s 违反基数约束", lnk.RID)
	}
	cp := lnk
	s.links[lnk.RID] = &cp
	s.indexAddLocked(lnk.SourceRID, lnk.TypeRID, lnk.TargetRID)
	return nil
}

// GetObject 读取对象（RLock；Atomic 导入期间会被阻塞，见 DESIGN.md 隔离边界）。
func (s *Store) GetObject(rid string) (ObjectInstance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[rid]
	if !ok {
		return ObjectInstance{}, false
	}
	return *obj, true
}

func (s *Store) GetLink(rid string) (LinkInstance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lnk, ok := s.links[rid]
	if !ok {
		return LinkInstance{}, false
	}
	return *lnk, true
}

// LinkCount 以 O(1) 索引访问返回 (源, 类型) 的已有链接数。
func (s *Store) LinkCount(sourceRID, linkTypeRID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.linkCountLocked(sourceRID, linkTypeRID)
}

// ListLinks 遍历某实例的全部已有链接。
// 这是唯一会随已有链接总数增长的操作，批量导入路径绝不调用它；
// 测试通过 AdjacencyScans 计数证明这一点。
func (s *Store) ListLinks(sourceRID string) []LinkInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listLinksLocked(sourceRID)
}

func (s *Store) listLinksLocked(sourceRID string) []LinkInstance {
	s.stats.AdjacencyScans++
	var out []LinkInstance
	for _, l := range s.links {
		if l.SourceRID == sourceRID {
			out = append(out, *l)
		}
	}
	return out
}

// ObjectCount / LinkCountAll 供测试断言最终图状态。
func (s *Store) ObjectCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.objects)
}

func (s *Store) LinkCountAll() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.links)
}

// Snapshot 返回当前对象与链接的完整拷贝，供测试比对最终图状态。
func (s *Store) Snapshot() (map[string]ObjectInstance, map[string]LinkInstance) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objs := make(map[string]ObjectInstance, len(s.objects))
	for k, v := range s.objects {
		objs[k] = *v
	}
	lnks := make(map[string]LinkInstance, len(s.links))
	for k, v := range s.links {
		lnks[k] = *v
	}
	return objs, lnks
}

// ---- 以下为导入器在持锁期间使用的内部方法 ----

func (s *Store) linkCountLocked(sourceRID, linkTypeRID string) int {
	s.stats.IndexOps++
	return len(s.linkIndex[linkKey{sourceRID, linkTypeRID}])
}

func (s *Store) indexAddLocked(sourceRID, linkTypeRID, targetRID string) {
	s.stats.IndexOps++
	k := linkKey{sourceRID, linkTypeRID}
	set, ok := s.linkIndex[k]
	if !ok {
		set = make(map[string]struct{})
		s.linkIndex[k] = set
	}
	set[targetRID] = struct{}{}
}

func (s *Store) indexRemoveLocked(sourceRID, linkTypeRID, targetRID string) {
	s.stats.IndexOps++
	k := linkKey{sourceRID, linkTypeRID}
	if set, ok := s.linkIndex[k]; ok {
		delete(set, targetRID)
		if len(set) == 0 {
			delete(s.linkIndex, k)
		}
	}
}

func (s *Store) putObjectLocked(obj *ObjectInstance) {
	s.objects[obj.RID] = obj
}

func (s *Store) deleteObjectLocked(rid string) error {
	if s.hooks.UndoError != nil {
		if err := s.hooks.UndoError(rid); err != nil {
			return err
		}
	}
	if _, ok := s.objects[rid]; !ok {
		return fmt.Errorf("对象 %s 不存在，无法撤销", rid)
	}
	delete(s.objects, rid)
	return nil
}

func (s *Store) putLinkLocked(lnk *LinkInstance) {
	s.links[lnk.RID] = lnk
	s.indexAddLocked(lnk.SourceRID, lnk.TypeRID, lnk.TargetRID)
}

func (s *Store) deleteLinkLocked(rid string) error {
	if s.hooks.UndoError != nil {
		if err := s.hooks.UndoError(rid); err != nil {
			return err
		}
	}
	lnk, ok := s.links[rid]
	if !ok {
		return fmt.Errorf("链接 %s 不存在，无法撤销", rid)
	}
	delete(s.links, rid)
	s.indexRemoveLocked(lnk.SourceRID, lnk.TypeRID, lnk.TargetRID)
	return nil
}
