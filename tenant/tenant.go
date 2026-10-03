package tenant

import (
	"errors"
	"sync"
)

var (
	ErrNotFound   = errors.New("不存在")
	ErrDuplicate  = errors.New("已存在")
	ErrBadRequest = errors.New("参数非法")
)

type Registry struct {
	mu sync.RWMutex

	principalTenant map[string]string
	bucketOwner     map[string]string
	knownTenant     map[string]bool
	suspended       map[string]bool
}

func New() *Registry { return &Registry{} }

func (r *Registry) RegisterPrincipal(p, t string) error {
	if p == "" || t == "" {
		return ErrBadRequest
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lazyInit()
	if _, ok := r.principalTenant[p]; ok {
		return ErrDuplicate
	}
	r.principalTenant[p] = t
	r.knownTenant[t] = true
	return nil
}

func (r *Registry) CreateBucket(owner, bucket string) error {
	if owner == "" || bucket == "" {
		return ErrBadRequest
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lazyInit()
	if _, ok := r.bucketOwner[bucket]; ok {
		return ErrDuplicate
	}
	r.bucketOwner[bucket] = owner
	r.knownTenant[owner] = true
	return nil
}

func (r *Registry) Suspend(t string) error {
	if t == "" {
		return ErrBadRequest
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lazyInit()
	if !r.knownTenant[t] {
		return ErrNotFound
	}
	r.suspended[t] = true
	return nil
}

func (r *Registry) Resume(t string) error {
	if t == "" {
		return ErrBadRequest
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lazyInit()
	if !r.knownTenant[t] {
		return ErrNotFound
	}
	delete(r.suspended, t)
	return nil
}

type Snapshot struct {
	PrincipalTenant map[string]string
	BucketOwner     map[string]string
	Suspended       map[string]bool
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	r.lazyInit()
	s := Snapshot{
		PrincipalTenant: make(map[string]string, len(r.principalTenant)),
		BucketOwner:     make(map[string]string, len(r.bucketOwner)),
		Suspended:       make(map[string]bool, len(r.suspended)),
	}
	for p, t := range r.principalTenant {
		s.PrincipalTenant[p] = t
	}
	for b, t := range r.bucketOwner {
		s.BucketOwner[b] = t
	}
	for t := range r.suspended {
		s.Suspended[t] = true
	}
	return s
}

func (r *Registry) lazyInit() {
	if r.principalTenant == nil {
		r.principalTenant = map[string]string{}
		r.bucketOwner = map[string]string{}
		r.knownTenant = map[string]bool{}
		r.suspended = map[string]bool{}
	}
}
