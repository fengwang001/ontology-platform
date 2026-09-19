package ontology

import (
	"fmt"
	"sync"
)

// Store 是本体平台关系子系统的核心：对象、LinkType 声明与双向链索引。
// 全部方法并发安全；任何时刻正向索引与反向索引互为镜像。
type Store struct {
	mu sync.RWMutex

	objectTypes map[string]struct{}
	objects     map[ObjectKey]struct{}
	linkTypes   map[string]LinkType

	// fwd[linkType][source] = 目标集合；bwd 为其镜像。
	fwd map[string]map[ObjectKey]map[ObjectKey]struct{}
	bwd map[string]map[ObjectKey]map[ObjectKey]struct{}
}

// NewStore 创建一个空 Store。
func NewStore() *Store {
	return &Store{
		objectTypes: make(map[string]struct{}),
		objects:     make(map[ObjectKey]struct{}),
		linkTypes:   make(map[string]LinkType),
		fwd:         make(map[string]map[ObjectKey]map[ObjectKey]struct{}),
		bwd:         make(map[string]map[ObjectKey]map[ObjectKey]struct{}),
	}
}

// RegisterObjectType 注册一个 ObjectType。
func (s *Store) RegisterObjectType(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return fmt.Errorf("object type name is empty")
	}
	if _, ok := s.objectTypes[name]; ok {
		return fmt.Errorf("object type %q already registered", name)
	}
	s.objectTypes[name] = struct{}{}
	return nil
}

// AddObject 添加一个对象实例。
func (s *Store) AddObject(typeName, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectTypes[typeName]; !ok {
		return fmt.Errorf("object type %q not registered", typeName)
	}
	key := ObjectKey{Type: typeName, ID: id}
	if _, ok := s.objects[key]; ok {
		return fmt.Errorf("object %v already exists", key)
	}
	s.objects[key] = struct{}{}
	return nil
}

// HasObject 报告对象是否存在。
func (s *Store) HasObject(key ObjectKey) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.objects[key]
	return ok
}

// DeclareLinkType 声明一个 LinkType；源/目标 ObjectType 必须已注册。
func (s *Store) DeclareLinkType(lt LinkType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lt.Name == "" {
		return fmt.Errorf("link type name is empty")
	}
	if _, ok := s.linkTypes[lt.Name]; ok {
		return fmt.Errorf("link type %q already declared", lt.Name)
	}
	if _, ok := s.objectTypes[lt.SourceType]; !ok {
		return fmt.Errorf("source object type %q not registered", lt.SourceType)
	}
	if _, ok := s.objectTypes[lt.TargetType]; !ok {
		return fmt.Errorf("target object type %q not registered", lt.TargetType)
	}
	s.linkTypes[lt.Name] = lt
	return nil
}

// LinkType 返回已声明的 LinkType。
func (s *Store) LinkType(name string) (LinkType, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lt, ok := s.linkTypes[name]
	return lt, ok
}

// snapshot 是用于原子回滚的状态副本。
type snapshot struct {
	objects map[ObjectKey]struct{}
	fwd     map[string]map[ObjectKey]map[ObjectKey]struct{}
	bwd     map[string]map[ObjectKey]map[ObjectKey]struct{}
}

func cloneIndex(src map[string]map[ObjectKey]map[ObjectKey]struct{}) map[string]map[ObjectKey]map[ObjectKey]struct{} {
	out := make(map[string]map[ObjectKey]map[ObjectKey]struct{}, len(src))
	for lt, bySrc := range src {
		nb := make(map[ObjectKey]map[ObjectKey]struct{}, len(bySrc))
		for k, set := range bySrc {
			ns := make(map[ObjectKey]struct{}, len(set))
			for v := range set {
				ns[v] = struct{}{}
			}
			nb[k] = ns
		}
		out[lt] = nb
	}
	return out
}

func (s *Store) snapshotLocked() *snapshot {
	objects := make(map[ObjectKey]struct{}, len(s.objects))
	for k := range s.objects {
		objects[k] = struct{}{}
	}
	return &snapshot{objects: objects, fwd: cloneIndex(s.fwd), bwd: cloneIndex(s.bwd)}
}

func (s *Store) restoreLocked(snap *snapshot) {
	s.objects = snap.objects
	s.fwd = snap.fwd
	s.bwd = snap.bwd
}
