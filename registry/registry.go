package registry

import (
	"errors"
	"sync"
)

var (
	ErrNotFound = errors.New("registry: bucket not found")
	ErrExists   = errors.New("registry: bucket already exists")
)

// Bucket 是桶元数据快照。Objects 仅用于证明转移不触碰对象记录。
type Bucket struct {
	Name    string
	Owner   string
	Bytes   int64
	Uploads int64
	Objects int64
}

type Registry struct {
	mu      sync.RWMutex
	buckets map[string]*Bucket
}

func New() *Registry {
	return &Registry{buckets: map[string]*Bucket{}}
}

func (r *Registry) Create(name, owner string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.buckets[name]; ok {
		return ErrExists
	}
	r.buckets[name] = &Bucket{Name: name, Owner: owner}
	return nil
}

func (r *Registry) Get(name string) (Bucket, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.buckets[name]
	if !ok {
		return Bucket{}, ErrNotFound
	}
	return *b, nil
}

func (r *Registry) AddBytes(name string, delta int64) (Bucket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[name]
	if !ok {
		return Bucket{}, ErrNotFound
	}
	b.Bytes += delta
	if delta > 0 {
		b.Objects++
	}
	return *b, nil
}

func (r *Registry) IncUploads(name string) (Bucket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[name]
	if !ok {
		return Bucket{}, ErrNotFound
	}
	b.Uploads++
	return *b, nil
}

func (r *Registry) DecUploads(name string) (Bucket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[name]
	if !ok {
		return Bucket{}, ErrNotFound
	}
	if b.Uploads == 0 {
		return Bucket{}, errors.New("registry: upload counter already zero")
	}
	b.Uploads--
	return *b, nil
}

// Reown 把属主改为 to，返回转移前快照（字节数取自该快照）。
func (r *Registry) Reown(name, to string) (Bucket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[name]
	if !ok {
		return Bucket{}, ErrNotFound
	}
	old := *b
	b.Owner = to
	return old, nil
}

func (r *Registry) Snapshot() map[string]Bucket {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Bucket, len(r.buckets))
	for name, b := range r.buckets {
		out[name] = *b
	}
	return out
}
