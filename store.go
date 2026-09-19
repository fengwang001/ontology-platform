package ontology

import (
	"sync"
	"time"
)

// Validator 在写入前校验属性，返回非 nil 错误即视为违反约束。
type Validator func(objectType string, props map[string]any) error

// record 是存储内部条目；deleted 为 true 时是墓碑，版本历史保留。
type record struct {
	version   int64
	updatedAt time.Time
	deleted   bool
	props     map[string]any
}

// Store 是进程内存中的实例存储，所有方法并发安全。
type Store struct {
	mu       sync.Mutex
	records  map[string]map[string]*record // objectType -> primaryKey -> record
	validate Validator
	now      func() time.Time
}

// NewStore 创建空存储；validate 可为 nil（不校验约束）。
func NewStore(validate Validator) *Store {
	return &Store{
		records:  make(map[string]map[string]*record),
		validate: validate,
		now:      time.Now,
	}
}

func (s *Store) bucket(objectType string) map[string]*record {
	bkt, ok := s.records[objectType]
	if !ok {
		bkt = make(map[string]*record)
		s.records[objectType] = bkt
	}
	return bkt
}

func (s *Store) find(objectType, pk string) (*record, bool) {
	bkt, ok := s.records[objectType]
	if !ok {
		return nil, false
	}
	rec, ok := bkt[pk]
	return rec, ok
}

func (s *Store) checkProps(objectType, pk string, props map[string]any) error {
	if s.validate == nil {
		return nil
	}
	if err := s.validate(objectType, props); err != nil {
		return &ConstraintError{ObjectType: objectType, PrimaryKey: pk, Reason: err.Error()}
	}
	return nil
}

// Create 新建实例，版本从 1 开始。主键已存活时报 AlreadyExistsError；
// 主键处于逻辑删除状态时视为复活，版本从墓碑版本继续递增。
func (s *Store) Create(objectType, pk string, props map[string]any) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProps(objectType, pk, props); err != nil {
		return nil, err
	}
	rec, ok := s.find(objectType, pk)
	switch {
	case ok && !rec.deleted:
		return nil, &AlreadyExistsError{ObjectType: objectType, PrimaryKey: pk, Version: rec.version}
	case ok: // 复活：版本延续，绝不回到 1
		rec.version++
		rec.updatedAt = s.now()
		rec.deleted = false
		rec.props = cloneProps(props)
	default:
		rec = &record{version: 1, updatedAt: s.now(), props: cloneProps(props)}
		s.bucket(objectType)[pk] = rec
	}
	return toInstance(objectType, pk, rec), nil
}

// Get 返回实例的独立副本；已逻辑删除的实例不可见。
func (s *Store) Get(objectType, pk string) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.find(objectType, pk)
	if !ok || rec.deleted {
		return nil, &NotFoundError{ObjectType: objectType, PrimaryKey: pk}
	}
	return toInstance(objectType, pk, rec), nil
}

// Update 按期望版本更新；版本不符报 VersionConflictError，
// 已删除报 DeletedError，从未存在报 NotFoundError。
func (s *Store) Update(objectType, pk string, expected int64, props map[string]any) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.expectLive(objectType, pk, expected)
	if err != nil {
		return nil, err
	}
	if err := s.checkProps(objectType, pk, props); err != nil {
		return nil, err
	}
	rec.version++
	rec.updatedAt = s.now()
	rec.props = cloneProps(props)
	return toInstance(objectType, pk, rec), nil
}

// Delete 逻辑删除：实例从 Get 与遍历中消失，但版本历史保留。
func (s *Store) Delete(objectType, pk string, expected int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.expectLive(objectType, pk, expected)
	if err != nil {
		return err
	}
	rec.version++
	rec.updatedAt = s.now()
	rec.deleted = true
	rec.props = nil
	return nil
}

// expectLive 校验主键存活且版本匹配；调用方须持有锁。
func (s *Store) expectLive(objectType, pk string, expected int64) (*record, error) {
	rec, ok := s.find(objectType, pk)
	if !ok {
		return nil, &NotFoundError{ObjectType: objectType, PrimaryKey: pk}
	}
	if rec.deleted {
		return nil, &DeletedError{ObjectType: objectType, PrimaryKey: pk, Version: rec.version}
	}
	if rec.version != expected {
		return nil, &VersionConflictError{
			ObjectType: objectType, PrimaryKey: pk,
			Expected: expected, Actual: rec.version,
		}
	}
	return rec, nil
}

func toInstance(objectType, pk string, rec *record) *Instance {
	return (&Instance{
		ObjectType: objectType,
		PrimaryKey: pk,
		Version:    rec.version,
		UpdatedAt:  rec.updatedAt,
		Properties: rec.props,
	}).clone()
}
