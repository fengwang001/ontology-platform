// Package store 实现多租户管理：租户注册、按 id 路由、
// adminToken 校验与 Purge/SetQuota 管理操作。仅依赖 ten 包。
package store

import (
	"errors"
	"sync"

	"ontology/ten"
)

// 多租户层哨兵错误：彼此不同，也与 ten 层错误不同。
var (
	ErrNoTenant        = errors.New("store: tenant is not registered")
	ErrDuplicateTenant = errors.New("store: tenant already registered")
	ErrInvalidID       = errors.New("store: tenant id must not be empty")
	ErrInvalidQuota    = errors.New("store: quota values must be >= 0")
	ErrUnauthorized    = errors.New("store: admin token mismatch")
)

// ten 层错误原样重导出，使 api 只需依赖 store 即可判定全部错误。
var ErrEmptyKey, ErrNotFound, ErrQuotaKeys, ErrQuotaBytes = ten.ErrEmptyKey, ten.ErrNotFound, ten.ErrQuotaKeys, ten.ErrQuotaBytes

// TenantView 是某租户当前状态的只读快照（Items 为深拷贝）。
type TenantView struct {
	ID                                      string
	KeyCount, TotalBytes, MaxKeys, MaxBytes int
	Items                                   map[string]string
}

// Store 持有全部租户。一把 RWMutex 串行化访问，锁方向单一不会死锁。
type Store struct {
	mu      sync.RWMutex
	admin   string
	tenants map[string]*ten.Tenant
	order   []string
}

// New 创建空存储，adminToken 是管理操作凭据。
func New(adminToken string) *Store {
	return &Store{admin: adminToken, tenants: make(map[string]*ten.Tenant)}
}

// call 持锁（write 决定写/读锁）定位租户并执行 f；未注册返回 ErrNoTenant。
func call[R any](s *Store, id string, write bool, f func(*ten.Tenant) (R, error)) (R, error) {
	var zero R
	if write {
		s.mu.Lock()
		defer s.mu.Unlock()
	} else {
		s.mu.RLock()
		defer s.mu.RUnlock()
	}
	t, ok := s.tenants[id]
	if !ok {
		return zero, ErrNoTenant
	}
	return f(t)
}

func callErr(s *Store, id string, w bool, f func(*ten.Tenant) error) error {
	_, e := call(s, id, w, func(t *ten.Tenant) (struct{}, error) { return struct{}{}, f(t) })
	return e
}

// Register 创建租户；id 非空、不得重复、配额非负。
func (s *Store) Register(id string, maxKeys, maxBytes int) error {
	if id == "" {
		return ErrInvalidID
	}
	if maxKeys < 0 || maxBytes < 0 {
		return ErrInvalidQuota
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return ErrDuplicateTenant
	}
	s.tenants[id] = ten.New(maxKeys, maxBytes)
	s.order = append(s.order, id)
	return nil
}

func (s *Store) Put(id, key, val string) error {
	return callErr(s, id, true, func(t *ten.Tenant) error { return t.Put(key, val) })
}

func (s *Store) Get(id, key string) (string, error) {
	return call(s, id, false, func(t *ten.Tenant) (string, error) { return t.Get(key) })
}

func (s *Store) Del(id, key string) error {
	return callErr(s, id, true, func(t *ten.Tenant) error { return t.Del(key) })
}

func (s *Store) Usage(id string) (int, int, error) {
	r, err := call(s, id, false, func(t *ten.Tenant) ([2]int, error) {
		kc, tb := t.Usage()
		return [2]int{kc, tb}, nil
	})
	return r[0], r[1], err
}

// Purge 是管理操作：先验 token——错误 token 一律 ErrUnauthorized，
// 与租户是否存在无关——再清空。
func (s *Store) Purge(token, id string) error {
	if token != s.admin {
		return ErrUnauthorized
	}
	return callErr(s, id, true, func(t *ten.Tenant) error { t.Purge(); return nil })
}

// SetQuota 是管理操作：先验 token，再校验配额；新配额不得低于当前用量，
// 否则拒绝且原配额不变，以维持 keyCount<=maxKeys、totalBytes<=maxBytes。
func (s *Store) SetQuota(token, id string, maxKeys, maxBytes int) error {
	if token != s.admin {
		return ErrUnauthorized
	}
	if maxKeys < 0 || maxBytes < 0 {
		return ErrInvalidQuota
	}
	return callErr(s, id, true, func(t *ten.Tenant) error {
		kc, tb := t.Usage()
		if kc > maxKeys || tb > maxBytes {
			return ErrInvalidQuota
		}
		t.SetQuota(maxKeys, maxBytes)
		return nil
	})
}

// View 按注册顺序返回全部租户快照，Items 为深拷贝。
func (s *Store) View() []TenantView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TenantView, 0, len(s.order))
	for _, id := range s.order {
		t := s.tenants[id]
		kc, tb := t.Usage()
		mk, mb := t.Quota()
		out = append(out, TenantView{ID: id, KeyCount: kc, TotalBytes: tb,
			MaxKeys: mk, MaxBytes: mb, Items: t.Snapshot()})
	}
	return out
}
