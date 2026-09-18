package ontology

import (
	"fmt"
	"sync"
	"time"
)

// Instance 是一个对象实例。Version 从 1 开始单调递增（逻辑删除后复活也继续递增，
// 不会回到 1）；UpdatedAt 是最后一次成功写入的时间。
//
// Get 返回的 Instance 是独立副本：修改其 Properties（含嵌套 map / slice）
// 不影响存储；存储后续的写入也不会改变调用方已拿到的快照。
type Instance struct {
	ObjectType string
	PrimaryKey string
	Version    int64
	Properties map[string]any
	UpdatedAt  time.Time
}

// WriteOp 是 BatchWrite 中的单条操作。
//   - Create：使用 Properties；主键已存在（未删除）则失败；
//     主键曾被逻辑删除则视为复活，版本从删除前继续递增。
//   - Update：必须携带 ExpectedVersion 与 Properties。
//   - Delete：必须携带 ExpectedVersion；逻辑删除，版本历史保留。
type WriteOp struct {
	Kind            OpKind
	ObjectType      string
	PrimaryKey      string
	ExpectedVersion int64 // 仅 Update / Delete 使用
	Properties      map[string]any
}

// record 是存储内部记录；deleted 为 true 时实例不可见，但版本号保留。
type record struct {
	inst    Instance
	deleted bool
}

// Store 是进程内存中的实例存储，提供乐观并发控制。全部方法并发安全。
type Store struct {
	mu      sync.RWMutex
	objects map[string]map[string]*record // ObjectType -> PrimaryKey -> record
}

// NewStore 创建一个空的实例存储。
func NewStore() *Store {
	return &Store{objects: make(map[string]map[string]*record)}
}

func checkKey(objectType, primaryKey string) error {
	if objectType == "" {
		return fmt.Errorf("%w: object type must not be empty", ErrInvalidArgument)
	}
	if primaryKey == "" {
		return fmt.Errorf("%w: primary key must not be empty", ErrInvalidArgument)
	}
	return nil
}

// Create 创建实例，初始版本为 1。若主键曾被逻辑删除，则复活该实例，
// 版本从删除前的版本继续递增。
func (s *Store) Create(objectType, primaryKey string, props map[string]any) (*Instance, error) {
	if err := checkKey(objectType, primaryKey); err != nil {
		return nil, err
	}
	if err := validateProperties(props); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec := s.lookup(objectType, primaryKey); rec != nil && !rec.deleted {
		return nil, fmt.Errorf("%w: %s/%s", ErrAlreadyExists, objectType, primaryKey)
	}
	return s.applyCreate(objectType, primaryKey, props, time.Now()), nil
}

// Get 读取实例的独立副本。主键从未存在返回 ErrNotFound；
// 已被逻辑删除返回 ErrDeleted（两者是不同类别）。
func (s *Store) Get(objectType, primaryKey string) (*Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec := s.lookup(objectType, primaryKey)
	if rec == nil {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, objectType, primaryKey)
	}
	if rec.deleted {
		return nil, fmt.Errorf("%w: %s/%s", ErrDeleted, objectType, primaryKey)
	}
	return copyInstance(&rec.inst), nil
}

// Update 按期望版本更新实例。版本不匹配返回 *VersionConflictError；
// 主键从未存在返回 ErrNotFound；已被逻辑删除返回 ErrDeleted。
func (s *Store) Update(objectType, primaryKey string, expectedVersion int64, props map[string]any) (*Instance, error) {
	if err := checkKey(objectType, primaryKey); err != nil {
		return nil, err
	}
	if err := validateProperties(props); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.checkVersion(objectType, primaryKey, expectedVersion)
	if err != nil {
		return nil, err
	}
	return s.applyUpdate(rec, props, time.Now()), nil
}

// Delete 按期望版本逻辑删除实例。版本不匹配返回 *VersionConflictError；
// 主键从未存在返回 ErrNotFound；已被逻辑删除返回 ErrDeleted。
// 删除后版本号历史保留，Get 与遍历均不可见该实例。
func (s *Store) Delete(objectType, primaryKey string, expectedVersion int64) error {
	if err := checkKey(objectType, primaryKey); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.checkVersion(objectType, primaryKey, expectedVersion)
	if err != nil {
		return err
	}
	rec.deleted = true
	rec.inst.UpdatedAt = time.Now()
	return nil
}

// BatchWrite 原子地应用一批混合的 Create / Update / Delete。
// 任意一条失败（版本冲突、主键在批内重复、属性违反约束等），
// 整批不生效：已校验通过的部分不会落盘，版本号不前进，最后写入时间不改变。
// 失败时返回 *BatchError，指出第几条、哪个主键、哪一类原因。
func (s *Store) BatchWrite(ops []WriteOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 第一阶段：完整校验。批内不允许同一 (ObjectType, PrimaryKey) 出现多次，
	// 因此各操作相互独立，全部校验通过后应用阶段不会失败。
	seen := make(map[[2]string]int, len(ops))
	for i, op := range ops {
		if err := s.validateOp(i, op, seen); err != nil {
			return err
		}
		seen[[2]string{op.ObjectType, op.PrimaryKey}] = i
	}

	// 第二阶段：应用。整批在锁内完成，其他 goroutine 看不到中间状态。
	now := time.Now()
	for _, op := range ops {
		switch op.Kind {
		case OpCreate:
			s.applyCreate(op.ObjectType, op.PrimaryKey, op.Properties, now)
		case OpUpdate:
			rec := s.lookup(op.ObjectType, op.PrimaryKey)
			s.applyUpdate(rec, op.Properties, now)
		case OpDelete:
			rec := s.lookup(op.ObjectType, op.PrimaryKey)
			rec.deleted = true
			rec.inst.UpdatedAt = now
		}
	}
	return nil
}

// validateOp 校验单条批量操作（不修改任何状态）。
// 任何失败都包装为 *BatchError，携带序号、主键与原因类别。
func (s *Store) validateOp(index int, op WriteOp, seen map[[2]string]int) error {
	fail := func(err error) error {
		return &BatchError{
			Index:      index,
			Op:         op.Kind,
			ObjectType: op.ObjectType,
			PrimaryKey: op.PrimaryKey,
			Err:        err,
		}
	}
	if op.Kind != OpCreate && op.Kind != OpUpdate && op.Kind != OpDelete {
		return fail(fmt.Errorf("%w: unknown op kind %d", ErrInvalidArgument, op.Kind))
	}
	if err := checkKey(op.ObjectType, op.PrimaryKey); err != nil {
		return fail(err)
	}
	// 同一主键在同一批里出现多次：直接拒绝，不做顺序合并。
	if first, dup := seen[[2]string{op.ObjectType, op.PrimaryKey}]; dup {
		return fail(fmt.Errorf("%w: %s/%s (first at op %d)",
			ErrDuplicateInBatch, op.ObjectType, op.PrimaryKey, first))
	}
	switch op.Kind {
	case OpCreate:
		if err := validateProperties(op.Properties); err != nil {
			return fail(err)
		}
		if rec := s.lookup(op.ObjectType, op.PrimaryKey); rec != nil && !rec.deleted {
			return fail(fmt.Errorf("%w: %s/%s", ErrAlreadyExists, op.ObjectType, op.PrimaryKey))
		}
	case OpUpdate:
		if err := validateProperties(op.Properties); err != nil {
			return fail(err)
		}
		if _, err := s.checkVersion(op.ObjectType, op.PrimaryKey, op.ExpectedVersion); err != nil {
			return fail(err)
		}
	case OpDelete:
		if _, err := s.checkVersion(op.ObjectType, op.PrimaryKey, op.ExpectedVersion); err != nil {
			return fail(err)
		}
	}
	return nil
}

func (s *Store) lookup(objectType, primaryKey string) *record {
	if m, ok := s.objects[objectType]; ok {
		return m[primaryKey]
	}
	return nil
}

// checkVersion 返回记录并校验其可见性与期望版本。
func (s *Store) checkVersion(objectType, primaryKey string, expectedVersion int64) (*record, error) {
	rec := s.lookup(objectType, primaryKey)
	if rec == nil {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, objectType, primaryKey)
	}
	if rec.deleted {
		return nil, fmt.Errorf("%w: %s/%s", ErrDeleted, objectType, primaryKey)
	}
	if rec.inst.Version != expectedVersion {
		return nil, &VersionConflictError{
			ObjectType: objectType,
			PrimaryKey: primaryKey,
			Expected:   expectedVersion,
			Actual:     rec.inst.Version,
		}
	}
	return rec, nil
}

// applyCreate 写入新实例或复活已删除实例，版本从既有版本继续递增。
// 调用方必须已完成校验并持有写锁。
func (s *Store) applyCreate(objectType, primaryKey string, props map[string]any, now time.Time) *Instance {
	m, ok := s.objects[objectType]
	if !ok {
		m = make(map[string]*record)
		s.objects[objectType] = m
	}
	var version int64 = 1
	if rec := m[primaryKey]; rec != nil { // 已逻辑删除：复活，版本延续
		version = rec.inst.Version + 1
	}
	rec := &record{inst: Instance{
		ObjectType: objectType,
		PrimaryKey: primaryKey,
		Version:    version,
		Properties: deepCopyProperties(props),
		UpdatedAt:  now,
	}}
	m[primaryKey] = rec
	return copyInstance(&rec.inst)
}

// applyUpdate 在版本校验通过后应用更新。调用方必须持有写锁。
func (s *Store) applyUpdate(rec *record, props map[string]any, now time.Time) *Instance {
	rec.inst.Version++
	rec.inst.Properties = deepCopyProperties(props)
	rec.inst.UpdatedAt = now
	return copyInstance(&rec.inst)
}

// copyInstance 返回实例的独立副本（属性深拷贝）。
func copyInstance(inst *Instance) *Instance {
	cp := *inst
	cp.Properties = deepCopyProperties(inst.Properties)
	return &cp
}

func deepCopyProperties(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyValue(v)
	}
	return dst
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyProperties(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}

// validateProperties 校验属性约束：属性名非空，值必须是受支持的类型
// （nil、bool、字符串、数值，以及由它们递归组成的 map[string]any / []any）。
func validateProperties(props map[string]any) error {
	for k, v := range props {
		if k == "" {
			return fmt.Errorf("%w: property name must not be empty", ErrConstraintViolation)
		}
		if err := validateValue(v); err != nil {
			return fmt.Errorf("%w: property %q: %v", ErrConstraintViolation, k, err)
		}
	}
	return nil
}

func validateValue(v any) error {
	switch t := v.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	case map[string]any:
		return validateProperties(t)
	case []any:
		for _, e := range t {
			if err := validateValue(e); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported value type %T", v)
	}
}
