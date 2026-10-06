package layercfg

import "sync"

// Store 是线程安全的分层配置存储：模式登记、原子发布、回滚、按版本解析。
type Store struct {
	mu      sync.RWMutex
	schemas map[string]Schema
	current *snapshot
	history []*snapshot // history[v] 即版本 v 的不可变快照；只追加，不删除
}

// NewStore 创建存储，初始版本为 0 且配置为空。
func NewStore() *Store {
	base := initialSnapshot()
	return &Store{
		schemas: map[string]Schema{},
		current: base,
		history: []*snapshot{base},
	}
}

// RegisterKey 登记或更新键模式；模式不受版本影响，始终以最新登记为准。
func (s *Store) RegisterKey(spec SchemaSpec) error {
	schema, err := validateSpec(spec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemas[spec.Key] = schema
	return nil
}

// Schema 返回某键当前登记的模式。
func (s *Store) Schema(key string) (Schema, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	schema, ok := s.schemas[key]
	return schema, ok
}

// Publish 原子应用一批变更：全部校验通过才产生新版本，否则状态不变。
// 返回发布后的新版本号。
func (s *Store) Publish(changes []Change) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cand, err := validatePublish(s.current, s.schemas, changes)
	if err != nil {
		return s.current.version, err // 校验失败：状态与版本均不变
	}
	cand.version = s.current.version + 1
	s.current = cand
	s.history = append(s.history, cand) // 仅新增一个快照根，未变键结构共享
	return cand.version, nil
}

// Rollback 回滚到某历史版本：生成内容完全相同的新版本。
// 目标为当前版本时为无操作且不产生新版本。
func (s *Store) Rollback(version int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version < 0 {
		return s.current.version, errorf(ErrInvalidArgument, "version must not be negative")
	}
	if version >= len(s.history) {
		return s.current.version, errorf(ErrVersionNotFound, "version %d does not exist (current=%d)", version, s.current.version)
	}
	if version == s.current.version {
		return s.current.version, nil
	}
	target := s.history[version]
	// 以最新模式重新做必填校验：历史上满足的版本可能因模式变更而不再满足。
	// 通过检查的方式是构造一个与目标内容相同、版本号递增的候选快照。
	for _, scope := range entityScopes(target) {
		for key, schema := range s.schemas {
			if !schema.Required {
				continue
			}
			if !resolveKey(target, schema, scope).Present {
				return s.current.version, errorf(ErrRequiredMissing,
					"rollback would leave required key %q unset in scope env=%q region=%q instance=%q",
					key, scope.Env, scope.Region, scope.Instance)
			}
		}
	}
	cand := &snapshot{version: s.current.version + 1, keys: target.keys}
	s.current = cand
	s.history = append(s.history, cand)
	return cand.version, nil
}

// Current 返回当前版本号。
func (s *Store) Current() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.version
}

// snapshotAt 取指定版本快照；version<0 表示当前版本。
func (s *Store) snapshotAt(version int) (*snapshot, error) {
	if version < 0 {
		return s.current, nil
	}
	if version >= len(s.history) {
		return nil, errorf(ErrVersionNotFound, "version %d does not exist (current=%d)", version, s.current.version)
	}
	return s.history[version], nil
}

// Resolve 在指定版本（version<0 表示当前版本）解析单键。
func (s *Store) Resolve(key string, scope Scope, version int) (Result, error) {
	if err := scopeValid(scope); err != nil {
		return Result{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	schema, ok := s.schemas[key]
	if !ok {
		return Result{}, errorf(ErrKeyNotRegistered, "key %q is not registered", key)
	}
	if version >= len(s.history) {
		return Result{}, errorf(ErrVersionNotFound, "version %d does not exist (current=%d)", version, s.current.version)
	}
	snap, err := s.snapshotAt(version)
	if err != nil {
		return Result{}, err
	}
	return resolveKey(snap, schema, scope), nil
}

// ResolveAll 在指定版本解析所有已登记键。
func (s *Store) ResolveAll(scope Scope, version int) (map[string]Result, error) {
	if err := scopeValid(scope); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, err := s.snapshotAt(version)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, errorf(ErrVersionNotFound, "version %d does not exist (current=%d)", version, s.current.version)
	}
	out := make(map[string]Result, len(s.schemas))
	for key, schema := range s.schemas {
		out[key] = resolveKey(snap, schema, scope)
	}
	return out, nil
}
