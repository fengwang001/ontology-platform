// Package softdelete 提供逻辑删除（软删）能力：删除只打标记、物理记录保留，
// 默认查询过滤软删对象；软删对象仍占用唯一键，只有物理删除才释放键；
// 复活可撤销软删并恢复可见。
package softdelete

import (
	"io"
	"log"
	"sort"
	"sync"
	"time"
)

// State 表示对象的生命周期状态。
type State int

const (
	// StateAlive 存活，默认查询可见。
	StateAlive State = iota
	// StateDeleted 软删，记录保留但默认查询不可见，仍占用唯一键。
	StateDeleted
	// StatePurged 已物理删除（墓碑），唯一键已释放，可同键重建。
	StatePurged
)

func (s State) String() string {
	switch s {
	case StateAlive:
		return "alive"
	case StateDeleted:
		return "soft-deleted"
	case StatePurged:
		return "physically-deleted"
	default:
		return "unknown"
	}
}

// Object 是软删组件管理的对象载荷，Key 为唯一约束键。
type Object struct {
	Key       string
	Data      any
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 为零值表示未软删。
	DeletedAt time.Time
}

// QueryOptions 控制查询是否包含软删对象。
type QueryOptions struct {
	// IncludeDeleted 为 true 时查询结果包含软删对象，始终不含已物理删除对象。
	IncludeDeleted bool
}

type record struct {
	obj   Object
	state State
}

// Store 是并发安全的软删对象存储。
type Store struct {
	mu     sync.RWMutex
	recs   map[string]*record
	logger *log.Logger
}

// New 创建 Store，w 为 nil 时默认写入 io.Discard（静默）。
func New(w io.Writer) *Store {
	if w == nil {
		w = io.Discard
	}
	return &Store{
		recs:   make(map[string]*record),
		logger: log.New(w, "softdelete: ", log.LstdFlags|log.Lmicroseconds),
	}
}

// Create 以唯一键创建存活对象；键被存活或软删对象占用时返回 ErrKeyOccupied。
func (s *Store) Create(key string, data any) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if rec := s.recs[key]; rec != nil {
		switch rec.state {
		case StatePurged:
			delete(s.recs, key)
		default:
			s.reject("create", key, rec.state, ErrKeyOccupied, "unique key still occupied by "+rec.state.String())
			return Object{}, ErrKeyOccupied
		}
	}
	obj := Object{Key: key, Data: data, CreatedAt: now, UpdatedAt: now}
	s.recs[key] = &record{obj: obj, state: StateAlive}
	s.allow("create", key, StateAlive, "no live or soft-deleted record holds the key")
	return cloneObject(obj), nil
}

// SoftDelete 给存活对象打删除标记；对象不存在或已物理删除、已软删时拒绝。
func (s *Store) SoftDelete(key string) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.requireRecord("soft-delete", key)
	if err != nil {
		return Object{}, err
	}
	switch rec.state {
	case StateDeleted:
		s.reject("soft-delete", key, rec.state, ErrAlreadyDeleted, "delete marker already present")
		return Object{}, ErrAlreadyDeleted
	case StatePurged:
		s.reject("soft-delete", key, rec.state, ErrPhysicallyDeleted, "record was purged and no longer holds the key")
		return Object{}, ErrPhysicallyDeleted
	}
	now := time.Now()
	rec.state = StateDeleted
	rec.obj.DeletedAt = now
	rec.obj.UpdatedAt = now
	s.allow("soft-delete", key, rec.state, "alive -> soft-deleted, record retained and key still occupied")
	return cloneObject(rec.obj), nil
}

// Restore 复活软删对象；对象未软删（存活/不存在/已物理删除）时拒绝。
func (s *Store) Restore(key string) (Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.requireRecord("restore", key)
	if err != nil {
		return Object{}, err
	}
	switch rec.state {
	case StateAlive:
		s.reject("restore", key, rec.state, ErrNotDeleted, "object is visible; nothing to restore")
		return Object{}, ErrNotDeleted
	case StatePurged:
		s.reject("restore", key, rec.state, ErrPhysicallyDeleted, "record was purged and cannot be restored")
		return Object{}, ErrPhysicallyDeleted
	}
	now := time.Now()
	rec.state = StateAlive
	rec.obj.DeletedAt = time.Time{}
	rec.obj.UpdatedAt = now
	s.allow("restore", key, rec.state, "soft-deleted -> alive, delete marker cleared and visibility recovered")
	return cloneObject(rec.obj), nil
}

// Purge 物理删除对象并释放唯一键；对象不存在或已物理删除时拒绝。
func (s *Store) Purge(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.requireRecord("purge", key)
	if err != nil {
		return err
	}
	if rec.state == StatePurged {
		s.reject("purge", key, rec.state, ErrPhysicallyDeleted, "record was already purged")
		return ErrPhysicallyDeleted
	}
	from := rec.state
	rec.state = StatePurged
	rec.obj = Object{Key: key}
	s.allow("purge", key, StatePurged, from.String()+" -> physically-deleted, unique key released")
	return nil
}

// Get 查询单个对象，默认排除软删对象。
func (s *Store) Get(key string, opts QueryOptions) (Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := s.recs[key]
	if rec == nil {
		s.reject("get", key, StatePurged, ErrNotFound, "no record for key")
		return Object{}, ErrNotFound
	}
	switch rec.state {
	case StatePurged:
		s.reject("get", key, rec.state, ErrPhysicallyDeleted, "record is a purge tombstone")
		return Object{}, ErrPhysicallyDeleted
	case StateDeleted:
		if !opts.IncludeDeleted {
			s.reject("get", key, rec.state, ErrNotFound, "default query excludes soft-deleted objects")
			return Object{}, ErrNotFound
		}
	}
	return cloneObject(rec.obj), nil
}

// List 列出对象，默认排除软删对象；返回结果按 Key 排序以保证确定性。
func (s *Store) List(opts QueryOptions) []Object {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Object, 0, len(s.recs))
	for _, rec := range s.recs {
		if rec.state == StateDeleted && !opts.IncludeDeleted {
			continue
		}
		if rec.state == StatePurged {
			continue
		}
		out = append(out, cloneObject(rec.obj))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// StateOf 返回对象当前状态；对象从未创建过时返回错误。
func (s *Store) StateOf(key string) (State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := s.recs[key]
	if rec == nil {
		return StatePurged, ErrNotFound
	}
	return rec.state, nil
}

// requireRecord 在持锁状态下解析记录并对“不存在 / 已物理删除”给出可区分错误。
func (s *Store) requireRecord(op, key string) (*record, error) {
	rec := s.recs[key]
	if rec == nil {
		s.reject(op, key, StatePurged, ErrNotFound, "no record for key")
		return nil, ErrNotFound
	}
	return rec, nil
}

func (s *Store) allow(op, key string, state State, reason string) {
	s.logger.Printf("object=%q op=%s decision=allow state=%s basis=%s", key, op, state, reason)
}

func (s *Store) reject(op, key string, state State, err error, reason string) {
	s.logger.Printf("object=%q op=%s decision=reject state=%s reason=%q basis=%s", key, op, state, err, reason)
}

func cloneObject(obj Object) Object {
	return obj
}
