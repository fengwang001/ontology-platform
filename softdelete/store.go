// Package softdelete 提供对象的逻辑删除（软删）能力。
//
// 软删只给对象打删除标记并物理保留记录：默认查询不可见，但唯一键仍被占用；
// 只有物理删除（Purge）才会释放唯一键。软删对象可通过 Restore 复活并恢复可见。
package softdelete

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

// State 表示对象在软删状态机中的生命周期状态。
type State int

const (
	// StateAlive 表示对象存活、默认查询可见。
	StateAlive State = iota
	// StateDeleted 表示对象已被软删，默认查询不可见，但仍占用唯一键。
	StateDeleted
)

// Object 是存储对外暴露的对象视图。
type Object[K comparable, V any] struct {
	Key      K
	Value    V
	State    State
	Revision int64
}

// QueryOptions 控制查询时是否包含软删对象。
type QueryOptions struct {
	// IncludeDeleted 为 true 时查询结果包含软删对象；默认只返回存活对象。
	IncludeDeleted bool
}

// Store 是支持软删语义的并发安全对象存储。
type Store[K comparable, V any] struct {
	mu      sync.RWMutex
	records map[K]*Object[K, V]
	logger  *slog.Logger
}

// New 创建一个空的软删存储。
func New[K comparable, V any]() *Store[K, V] {
	return NewWithLogger[K, V](slog.Default())
}

// NewWithLogger 创建使用指定 logger 的存储；logger 为 nil 时丢弃日志。
func NewWithLogger[K comparable, V any](logger *slog.Logger) *Store[K, V] {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Store[K, V]{
		records: make(map[K]*Object[K, V]),
		logger:  logger,
	}
}

// Create 创建存活对象；若唯一键已被存活或软删对象占用则拒绝。
func (s *Store[K, V]) Create(ctx context.Context, key K, value V) (*Object[K, V], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.records[key]; ok {
		s.reject(ctx, "create", key, existing.State,
			ReasonKeyOccupied, "record present in states alive/deleted retains unique key")
		return nil, newOpError("create", ReasonKeyOccupied)
	}
	obj := &Object[K, V]{Key: key, Value: value, State: StateAlive, Revision: 1}
	s.records[key] = obj
	s.accept(ctx, "create", obj, "key absent, no live or soft-deleted record holds it")
	return clone(obj), nil
}

// SoftDelete 对存活对象打删除标记；对象不存在或已物理删除时拒绝。
func (s *Store[K, V]) SoftDelete(ctx context.Context, key K) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.records[key]
	if !ok {
		s.reject(ctx, "soft_delete", key, -1,
			ReasonNotFound, "no record in map: never created or physically purged")
		return newOpError("soft_delete", ReasonNotFound)
	}
	if obj.State == StateDeleted {
		s.reject(ctx, "soft_delete", key, obj.State,
			ReasonAlreadyDeleted, "state=deleted, deletion marker already present")
		return newOpError("soft_delete", ReasonAlreadyDeleted)
	}
	obj.State = StateDeleted
	obj.Revision++
	s.accept(ctx, "soft_delete", obj, "state=alive, transition alive->deleted without removing record")
	return nil
}

// Restore 撤销软删并恢复可见；对象未处于软删状态时拒绝。
func (s *Store[K, V]) Restore(ctx context.Context, key K) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.records[key]
	if !ok {
		s.reject(ctx, "restore", key, -1,
			ReasonNotFound, "no record in map: never created or physically purged")
		return newOpError("restore", ReasonNotFound)
	}
	if obj.State == StateAlive {
		s.reject(ctx, "restore", key, obj.State,
			ReasonNotDeleted, "state=alive, no deletion marker to revoke")
		return newOpError("restore", ReasonNotDeleted)
	}
	obj.State = StateAlive
	obj.Revision++
	s.accept(ctx, "restore", obj, "state=deleted, transition deleted->alive restores visibility")
	return nil
}

// Purge 物理删除对象并释放唯一键；对象不存在时拒绝。
func (s *Store[K, V]) Purge(ctx context.Context, key K) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.records[key]
	if !ok {
		s.reject(ctx, "purge", key, -1,
			ReasonNotFound, "no record in map: never created or already physically purged")
		return newOpError("purge", ReasonNotFound)
	}
	state := obj.State
	revision := obj.Revision
	delete(s.records, key)
	s.logger.LogAttrs(ctx, slog.LevelInfo, "softdelete operation",
		slog.String("op", "purge"),
		slog.Any("key", key),
		slog.String("object_state", stateName(state)),
		slog.Int64("revision", revision),
		slog.String("decision", "accepted"),
		slog.String("basis", "record present, physical removal releases unique key"),
	)
	return nil
}

// Get 查询单个对象；默认不返回软删对象。
func (s *Store[K, V]) Get(ctx context.Context, key K, opts QueryOptions) (*Object[K, V], bool) {
	if err := ctx.Err(); err != nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.records[key]
	if !ok || (obj.State == StateDeleted && !opts.IncludeDeleted) {
		return nil, false
	}
	return clone(obj), true
}

// List 列出对象；默认排除软删对象。
func (s *Store[K, V]) List(ctx context.Context, opts QueryOptions) []*Object[K, V] {
	if err := ctx.Err(); err != nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Object[K, V], 0, len(s.records))
	for _, obj := range s.records {
		if obj.State == StateDeleted && !opts.IncludeDeleted {
			continue
		}
		result = append(result, clone(obj))
	}
	return result
}

func (s *Store[K, V]) accept(ctx context.Context, op string, obj *Object[K, V], basis string) {
	s.logger.LogAttrs(ctx, slog.LevelInfo, "softdelete operation",
		slog.String("op", op),
		slog.Any("key", obj.Key),
		slog.String("object_state", stateName(obj.State)),
		slog.Int64("revision", obj.Revision),
		slog.String("decision", "accepted"),
		slog.String("basis", basis),
	)
}

func (s *Store[K, V]) reject(ctx context.Context, op string, key K, observed State, reason Reason, basis string) {
	attrs := []slog.Attr{
		slog.String("op", op),
		slog.Any("key", key),
		slog.String("decision", "rejected"),
		slog.String("reason", string(reason)),
		slog.String("basis", basis),
	}
	if observed >= 0 {
		attrs = append(attrs, slog.String("object_state", stateName(observed)))
	}
	s.logger.LogAttrs(ctx, slog.LevelWarn, "softdelete operation", attrs...)
}

func clone[K comparable, V any](obj *Object[K, V]) *Object[K, V] {
	cp := *obj
	return &cp
}

func stateName(state State) string {
	switch state {
	case StateAlive:
		return "alive"
	case StateDeleted:
		return "deleted"
	default:
		return "absent"
	}
}
