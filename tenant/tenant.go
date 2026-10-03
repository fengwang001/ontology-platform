package tenant

import (
	"errors"
	"sync"
)

var (
	ErrExists   = errors.New("已存在")
	ErrNotFound = errors.New("不存在")
	ErrInvalid  = errors.New("参数非法")
)

// Registry 记录主体归属、桶属主以及租户的认识/暂停状态。
type Registry struct {
	mu         sync.RWMutex
	principals map[string]string // principal -> tenant
	buckets    map[string]string // bucket -> owner tenant
	suspended  map[string]bool
	tenants    map[string]struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		principals: make(map[string]string),
		buckets:    make(map[string]string),
		suspended:  make(map[string]bool),
		tenants:    make(map[string]struct{}),
	}
}

// RegisterPrincipal 登记主体及其唯一归属租户；租户在此时即被认识。
func (r *Registry) RegisterPrincipal(p, t string) error {
	if p == "" || t == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.principals[p]; ok {
		return ErrExists
	}
	r.principals[p] = t
	r.tenants[t] = struct{}{}
	return nil
}

// CreateBucket 登记桶及其属主租户；租户在此时即被认识。
func (r *Registry) CreateBucket(owner, bucket string) error {
	if owner == "" || bucket == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.buckets[bucket]; ok {
		return ErrExists
	}
	r.buckets[bucket] = owner
	r.tenants[owner] = struct{}{}
	return nil
}

// Suspend 暂停租户；对从未认识的租户报不存在，重复暂停幂等。
func (r *Registry) Suspend(t string) error {
	if t == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.knownLocked(t) {
		return ErrNotFound
	}
	r.suspended[t] = true
	return nil
}

// Resume 解除暂停；对从未认识的租户报不存在，重复解除幂等。
func (r *Registry) Resume(t string) error {
	if t == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.knownLocked(t) {
		return ErrNotFound
	}
	delete(r.suspended, t)
	return nil
}

// TenantOf 返回主体所属租户；主体未登记时 ok 为 false。
func (r *Registry) TenantOf(p string) (t string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok = r.principals[p]
	return
}

// BucketOwner 返回桶属主租户；桶不存在时 ok 为 false。
func (r *Registry) BucketOwner(bucket string) (owner string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner, ok = r.buckets[bucket]
	return
}

// IsSuspended 报告租户当前是否暂停。
func (r *Registry) IsSuspended(t string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.suspended[t]
}

func (r *Registry) knownLocked(t string) bool {
	_, ok := r.tenants[t]
	return ok
}
