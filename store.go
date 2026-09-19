package ontology

import "sync"

// Store 是进程内的本体状态存储，带互斥锁。
type Store struct {
	mu sync.Mutex

	objects   map[string]*Object
	relations map[Relation]struct{}

	// inconsistentStep 非空表示存储已不一致，之后一切写入被拒绝。
	inconsistentStep string
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		objects:   make(map[string]*Object),
		relations: make(map[Relation]struct{}),
	}
}

// Lock / Unlock 供引擎在一次事务期间持有存储锁。
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

func (s *Store) checkWritable() error {
	if s.inconsistentStep != "" {
		return &InconsistentError{Step: s.inconsistentStep}
	}
	return nil
}

// markInconsistent 在撤销失败时冻结存储。
func (s *Store) markInconsistent(step string) {
	if s.inconsistentStep == "" {
		s.inconsistentStep = step
	}
}

// InconsistentStep 返回导致不一致的步骤描述；一致时返回空串。
func (s *Store) InconsistentStep() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inconsistentStep
}

// IsConsistent 报告存储是否处于一致状态。
func (s *Store) IsConsistent() bool {
	return s.InconsistentStep() == ""
}

// ObjectCount / RelationCount 用于状态对照与测试。
func (s *Store) ObjectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

func (s *Store) RelationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.relations)
}

// GetObject 在调用方已持锁或只读快照场景下使用。
func (s *Store) GetObject(id string) (*Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	if !ok {
		return nil, false
	}
	cp := *o
	cp.Attributes = copyAttrs(o.Attributes)
	return &cp, true
}
