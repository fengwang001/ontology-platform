// Package ontology 提供本体平台的实例存储与乐观并发控制。
//
// 存储对外只暴露 Create / Get / Update / Delete / BatchWrite 五个操作，
// 状态保存在进程内存中，全部操作并发安全。
package ontology

import (
	"sync"
	"time"
)

// Instance 是对象实例的一份独立快照。修改它不会影响存储内容。
type Instance struct {
	ObjectType string
	PrimaryKey string
	Properties map[string]any
	Version    int64 // 单调递增，从 1 开始
	UpdatedAt  time.Time
}

// OpKind 标识批次中的写操作类型。
type OpKind int

const (
	OpCreate OpKind = iota
	OpUpdate
	OpDelete
)

// WriteOp 是 BatchWrite 中的一条写操作。
type WriteOp struct {
	Kind            OpKind
	ObjectType      string
	PrimaryKey      string
	ExpectedVersion int64          // 仅 OpUpdate / OpDelete 使用
	Properties      map[string]any // 仅 OpCreate / OpUpdate 使用
}

// record 是存储内部持有的实例记录，逻辑删除后仍保留版本历史。
type record struct {
	properties map[string]any
	version    int64
	updatedAt  time.Time
	deleted    bool
}

func (r *record) snapshot(objectType, primaryKey string) *Instance {
	return &Instance{
		ObjectType: objectType,
		PrimaryKey: primaryKey,
		Properties: deepCopyProperties(r.properties),
		Version:    r.version,
		UpdatedAt:  r.updatedAt,
	}
}

// Store 是并发安全的内存实例存储。
type Store struct {
	mu      sync.RWMutex
	tables  map[string]map[string]*record // objectType -> primaryKey -> record
	nowFunc func() time.Time
}

// NewStore 返回一个空存储。
func NewStore() *Store {
	return &Store{
		tables:  make(map[string]map[string]*record),
		nowFunc: time.Now,
	}
}

// Create 创建新实例，版本号从 1 开始。若主键被存活实例占用则返回
// AlreadyExistsError；若主键属于已逻辑删除的实例则视为复活，
// 版本号从删除前的版本继续递增。
func (s *Store) Create(objectType, primaryKey string, properties map[string]any) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkCreate(objectType, primaryKey, properties); err != nil {
		return nil, err
	}
	return s.applyCreate(objectType, primaryKey, properties), nil
}

// Get 返回实例的独立副本。已逻辑删除的实例视为不存在。
func (s *Store) Get(objectType, primaryKey string) (*Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.tables[objectType][primaryKey]
	if !ok || rec.deleted {
		return nil, &NotFoundError{ObjectType: objectType, PrimaryKey: primaryKey}
	}
	return rec.snapshot(objectType, primaryKey), nil
}

// Update 以期望版本号整体替换实例属性。版本不匹配返回 ConflictError，
// 实例已被逻辑删除返回 DeletedError，主键从未存在返回 NotFoundError。
func (s *Store) Update(objectType, primaryKey string, expectedVersion int64, properties map[string]any) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUpdate(objectType, primaryKey, expectedVersion, properties); err != nil {
		return nil, err
	}
	return s.applyUpdate(objectType, primaryKey, properties), nil
}

// Delete 以期望版本号逻辑删除实例：实例不再出现在 Get 结果中，
// 但版本号历史保留，删除本身也会推进版本号。
func (s *Store) Delete(objectType, primaryKey string, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkDelete(objectType, primaryKey, expectedVersion); err != nil {
		return err
	}
	s.applyDelete(objectType, primaryKey)
	return nil
}

// BatchWrite 原子地应用一组混合的 Create/Update/Delete。
// 任一操作校验失败（版本冲突、批内主键重复、属性违反约束等），
// 整批不生效：版本号不前进，最后写入时间不改变。
// 同一主键在同一批里出现多次会被直接拒绝。
func (s *Store) BatchWrite(ops []WriteOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	type key struct {
		objectType string
		primaryKey string
	}
	seen := make(map[key]int, len(ops))
	for i, op := range ops {
		k := key{op.ObjectType, op.PrimaryKey}
		if first, dup := seen[k]; dup {
			return &BatchError{Index: i, ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey,
				Err: &DuplicateKeyError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, FirstIndex: first}}
		}
		seen[k] = i

		var err error
		switch op.Kind {
		case OpCreate:
			err = s.checkCreate(op.ObjectType, op.PrimaryKey, op.Properties)
		case OpUpdate:
			err = s.checkUpdate(op.ObjectType, op.PrimaryKey, op.ExpectedVersion, op.Properties)
		case OpDelete:
			err = s.checkDelete(op.ObjectType, op.PrimaryKey, op.ExpectedVersion)
		default:
			err = &ValidationError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey,
				Reason: "unknown operation kind"}
		}
		if err != nil {
			return &BatchError{Index: i, ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Err: err}
		}
	}

	// 全部校验通过后才应用，应用阶段不会失败，因此整批天然原子。
	for _, op := range ops {
		switch op.Kind {
		case OpCreate:
			s.applyCreate(op.ObjectType, op.PrimaryKey, op.Properties)
		case OpUpdate:
			s.applyUpdate(op.ObjectType, op.PrimaryKey, op.Properties)
		case OpDelete:
			s.applyDelete(op.ObjectType, op.PrimaryKey)
		}
	}
	return nil
}

func (s *Store) checkCreate(objectType, primaryKey string, properties map[string]any) error {
	if err := validateIdentity(objectType, primaryKey); err != nil {
		return err
	}
	if err := validateProperties(objectType, primaryKey, properties); err != nil {
		return err
	}
	if rec, ok := s.tables[objectType][primaryKey]; ok && !rec.deleted {
		return &AlreadyExistsError{ObjectType: objectType, PrimaryKey: primaryKey, Version: rec.version}
	}
	return nil
}

func (s *Store) applyCreate(objectType, primaryKey string, properties map[string]any) *Instance {
	table := s.tables[objectType]
	if table == nil {
		table = make(map[string]*record)
		s.tables[objectType] = table
	}
	if rec, ok := table[primaryKey]; ok {
		// 复活已逻辑删除的实例：版本号从删除前的版本继续递增。
		rec.properties = deepCopyProperties(properties)
		rec.version++
		rec.updatedAt = s.nowFunc()
		rec.deleted = false
		return rec.snapshot(objectType, primaryKey)
	}
	rec := &record{
		properties: deepCopyProperties(properties),
		version:    1,
		updatedAt:  s.nowFunc(),
	}
	table[primaryKey] = rec
	return rec.snapshot(objectType, primaryKey)
}

func (s *Store) checkUpdate(objectType, primaryKey string, expectedVersion int64, properties map[string]any) error {
	if err := validateIdentity(objectType, primaryKey); err != nil {
		return err
	}
	if err := validateProperties(objectType, primaryKey, properties); err != nil {
		return err
	}
	rec, err := s.liveRecord(objectType, primaryKey)
	if err != nil {
		return err
	}
	if rec.version != expectedVersion {
		return &ConflictError{ObjectType: objectType, PrimaryKey: primaryKey,
			Expected: expectedVersion, Actual: rec.version}
	}
	return nil
}

func (s *Store) applyUpdate(objectType, primaryKey string, properties map[string]any) *Instance {
	rec := s.tables[objectType][primaryKey]
	rec.properties = deepCopyProperties(properties)
	rec.version++
	rec.updatedAt = s.nowFunc()
	return rec.snapshot(objectType, primaryKey)
}

func (s *Store) checkDelete(objectType, primaryKey string, expectedVersion int64) error {
	if err := validateIdentity(objectType, primaryKey); err != nil {
		return err
	}
	rec, err := s.liveRecord(objectType, primaryKey)
	if err != nil {
		return err
	}
	if rec.version != expectedVersion {
		return &ConflictError{ObjectType: objectType, PrimaryKey: primaryKey,
			Expected: expectedVersion, Actual: rec.version}
	}
	return nil
}

func (s *Store) applyDelete(objectType, primaryKey string) {
	rec := s.tables[objectType][primaryKey]
	rec.properties = nil
	rec.version++
	rec.updatedAt = s.nowFunc()
	rec.deleted = true
}

// liveRecord 返回存活记录；已删除与从未存在是两种不同的错误类别。
func (s *Store) liveRecord(objectType, primaryKey string) (*record, error) {
	rec, ok := s.tables[objectType][primaryKey]
	if !ok {
		return nil, &NotFoundError{ObjectType: objectType, PrimaryKey: primaryKey}
	}
	if rec.deleted {
		return nil, &DeletedError{ObjectType: objectType, PrimaryKey: primaryKey, Version: rec.version}
	}
	return rec, nil
}

func validateIdentity(objectType, primaryKey string) error {
	if objectType == "" {
		return &ValidationError{ObjectType: objectType, PrimaryKey: primaryKey,
			Reason: "object type must not be empty"}
	}
	if primaryKey == "" {
		return &ValidationError{ObjectType: objectType, PrimaryKey: primaryKey,
			Reason: "primary key must not be empty"}
	}
	return nil
}

func validateProperties(objectType, primaryKey string, properties map[string]any) error {
	for name, value := range properties {
		if name == "" {
			return &ValidationError{ObjectType: objectType, PrimaryKey: primaryKey,
				Reason: "property name must not be empty"}
		}
		if err := validateValue(objectType, primaryKey, name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(objectType, primaryKey, property string, value any) error {
	switch v := value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	case []any:
		for _, item := range v {
			if err := validateValue(objectType, primaryKey, property, item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for name, item := range v {
			if name == "" {
				return &ValidationError{ObjectType: objectType, PrimaryKey: primaryKey,
					Property: property, Reason: "nested property name must not be empty"}
			}
			if err := validateValue(objectType, primaryKey, property, item); err != nil {
				return err
			}
		}
		return nil
	default:
		return &ValidationError{ObjectType: objectType, PrimaryKey: primaryKey,
			Property: property, Reason: "unsupported property value type"}
	}
}

// deepCopyProperties 深拷贝属性 map，嵌套的 map 与切片一并拷贝，
// 保证存储与调用方之间双向隔离。
func deepCopyProperties(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyValue(v)
	}
	return dst
}

func deepCopyValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return deepCopyProperties(v)
	case []any:
		dst := make([]any, len(v))
		for i, item := range v {
			dst[i] = deepCopyValue(item)
		}
		return dst
	default:
		return v
	}
}
