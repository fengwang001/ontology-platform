package ontology

import (
	"sync"
	"time"
)

// Store 是进程内存中的实例存储，提供乐观并发控制。
// 所有方法并发安全；写操作互斥，批操作整体原子。
type Store struct {
	mu      sync.RWMutex
	records map[objectKey]*record
}

type objectKey struct {
	objectType string
	primaryKey string
}

// record 保存实例的当前状态。deleted 为 true 时是逻辑删除的墓碑：
// 版本号历史保留，props 已清空。
type record struct {
	version   int64
	updatedAt time.Time
	deleted   bool
	props     map[string]any
}

func NewStore() *Store {
	return &Store{records: make(map[objectKey]*record)}
}

// Create 创建新实例，初始版本为 1。若主键已存在（未删除）返回
// AlreadyExistsError；若已被逻辑删除则复活，版本从删除前继续递增。
func (s *Store) Create(objectType, primaryKey string, properties map[string]any) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := WriteOp{Kind: OpCreate, ObjectType: objectType, PrimaryKey: primaryKey, Properties: properties}
	if err := s.checkLocked(op); err != nil {
		return nil, err
	}
	return s.applyLocked(op, time.Now()), nil
}

// Get 返回实例的独立副本。实例不存在或已被逻辑删除时返回 NotFoundError。
func (s *Store) Get(objectType, primaryKey string) (*Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[objectKey{objectType, primaryKey}]
	if !ok || rec.deleted {
		return nil, &NotFoundError{ObjectType: objectType, PrimaryKey: primaryKey}
	}
	return snapshot(objectType, primaryKey, rec), nil
}

// Update 按期望版本全量替换属性。版本不匹配返回 VersionConflictError；
// 实例已被逻辑删除返回 DeletedError；从未存在返回 NotFoundError。
func (s *Store) Update(objectType, primaryKey string, expectedVersion int64, properties map[string]any) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := WriteOp{Kind: OpUpdate, ObjectType: objectType, PrimaryKey: primaryKey, ExpectedVersion: expectedVersion, Properties: properties}
	if err := s.checkLocked(op); err != nil {
		return nil, err
	}
	return s.applyLocked(op, time.Now()), nil
}

// Delete 按期望版本逻辑删除实例。版本历史保留，实例不再出现在 Get 中。
func (s *Store) Delete(objectType, primaryKey string, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := WriteOp{Kind: OpDelete, ObjectType: objectType, PrimaryKey: primaryKey, ExpectedVersion: expectedVersion}
	if err := s.checkLocked(op); err != nil {
		return err
	}
	s.applyLocked(op, time.Now())
	return nil
}

// BatchWrite 原子地应用一组混合操作：全部成功才生效，任意一条失败
// （版本冲突、批内主键重复、属性违反约束等）整批回滚，版本号与最后
// 写入时间都不变。同一批中同一主键出现多次会被直接拒绝。
func (s *Store) BatchWrite(ops []WriteOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[objectKey]int, len(ops))
	for i, op := range ops {
		k := objectKey{op.ObjectType, op.PrimaryKey}
		if first, dup := seen[k]; dup {
			return batchErr(i, op, &DuplicateOperationError{
				ObjectType: op.ObjectType,
				PrimaryKey: op.PrimaryKey,
				FirstIndex: first,
			})
		}
		seen[k] = i
	}

	// 先整体校验再整体应用：批内主键互不重复，因此校验时看到的
	// 存储状态就是每条操作应用时的状态，校验通过则应用不会失败。
	for i, op := range ops {
		if err := s.checkLocked(op); err != nil {
			return batchErr(i, op, err)
		}
	}

	now := time.Now()
	for _, op := range ops {
		s.applyLocked(op, now)
	}
	return nil
}

func batchErr(index int, op WriteOp, err error) error {
	return &BatchError{
		Index:      index,
		Op:         op.Kind,
		ObjectType: op.ObjectType,
		PrimaryKey: op.PrimaryKey,
		Err:        err,
	}
}

// checkLocked 校验单条操作在当前状态下能否应用，不做任何修改。
// 调用方必须持有写锁。
func (s *Store) checkLocked(op WriteOp) error {
	if op.ObjectType == "" {
		return &ConstraintError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Field: "objectType", Reason: "must not be empty"}
	}
	if op.PrimaryKey == "" {
		return &ConstraintError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Field: "primaryKey", Reason: "must not be empty"}
	}

	k := objectKey{op.ObjectType, op.PrimaryKey}
	rec, exists := s.records[k]

	switch op.Kind {
	case OpCreate:
		if err := validateProperties(op.ObjectType, op.PrimaryKey, op.Properties); err != nil {
			return err
		}
		if exists && !rec.deleted {
			return &AlreadyExistsError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Version: rec.version}
		}
		return nil
	case OpUpdate, OpDelete:
		if !exists {
			return &NotFoundError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey}
		}
		if rec.deleted {
			return &DeletedError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Version: rec.version}
		}
		if rec.version != op.ExpectedVersion {
			return &VersionConflictError{
				ObjectType: op.ObjectType,
				PrimaryKey: op.PrimaryKey,
				Expected:   op.ExpectedVersion,
				Actual:     rec.version,
			}
		}
		if op.Kind == OpUpdate {
			return validateProperties(op.ObjectType, op.PrimaryKey, op.Properties)
		}
		return nil
	default:
		return &ConstraintError{ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Field: "kind", Reason: "unknown operation kind"}
	}
}

// applyLocked 应用一条已通过校验的操作，返回新的实例快照（删除返回 nil）。
// 调用方必须持有写锁。
func (s *Store) applyLocked(op WriteOp, now time.Time) *Instance {
	k := objectKey{op.ObjectType, op.PrimaryKey}
	rec, exists := s.records[k]
	if !exists {
		rec = &record{}
		s.records[k] = rec
	}
	rec.version++
	rec.updatedAt = now

	if op.Kind == OpDelete {
		rec.deleted = true
		rec.props = nil
		return nil
	}
	rec.deleted = false
	rec.props = copyProperties(op.Properties)
	return snapshot(op.ObjectType, op.PrimaryKey, rec)
}

// snapshot 构造一条记录的独立副本。
func snapshot(objectType, primaryKey string, rec *record) *Instance {
	return &Instance{
		ObjectType: objectType,
		PrimaryKey: primaryKey,
		Version:    rec.version,
		UpdatedAt:  rec.updatedAt,
		Properties: copyProperties(rec.props),
	}
}

// copyProperties 深拷贝属性 map，切片等可变值一并复制，
// 保证存储与调用方之间双向隔离。
func copyProperties(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = copyValue(v)
	}
	return dst
}

func copyValue(v any) any {
	switch t := v.(type) {
	case []string:
		c := make([]string, len(t))
		copy(c, t)
		return c
	case []any:
		c := make([]any, len(t))
		for i, e := range t {
			c[i] = copyValue(e)
		}
		return c
	default:
		return v
	}
}

// validateProperties 校验属性约束：键非空，值为受支持的标量或切片类型。
func validateProperties(objectType, primaryKey string, props map[string]any) error {
	for name, v := range props {
		if name == "" {
			return &ConstraintError{ObjectType: objectType, PrimaryKey: primaryKey, Field: name, Reason: "property name must not be empty"}
		}
		if !validValue(v) {
			return &ConstraintError{ObjectType: objectType, PrimaryKey: primaryKey, Field: name, Reason: "unsupported property value type"}
		}
	}
	return nil
}

func validValue(v any) bool {
	switch t := v.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	case []string:
		return true
	case []any:
		for _, e := range t {
			if !validValue(e) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
