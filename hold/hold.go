// Package hold 实现法律保全（legal hold）登记表。
package hold

import (
	"errors"
	"sort"
	"sync"
)

// 参数与操作错误。
var (
	ErrInvalid    = errors.New("hold: invalid argument")
	ErrDuplicate  = errors.New("hold: duplicate hold id")
	ErrNotFound   = errors.New("hold: id not found")
	ErrPermission = errors.New("hold: only owner or admin may release")
)

// Interval 是半开保全区间 [From, To)。
type Interval struct {
	From  int64
	To    int64
	Owner string
}

// Registry 是线程安全的保全登记表。
type Registry struct {
	mu sync.Mutex
	m  map[string]Interval
}

func NewRegistry() *Registry { return &Registry{m: map[string]Interval{}} }

// Hold 登记 [from, to) 的保全。校验顺序：参数非法、重复。
func (r *Registry) Hold(id string, from, to int64, owner string) error {
	if id == "" || owner == "" || from < 0 || to > maxTS || from >= to {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[id]; ok {
		return ErrDuplicate
	}
	r.m[id] = Interval{From: from, To: to, Owner: owner}
	return nil
}

// Release 解除保全；仅 owner 或字面量 "admin" 可以解除。
// 校验顺序：参数非法、不存在、权限。
func (r *Registry) Release(id, who string) error {
	if id == "" || who == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.m[id]
	if !ok {
		return ErrNotFound
	}
	if who != "admin" && who != h.Owner {
		return ErrPermission
	}
	delete(r.m, id)
	return nil
}

// Intersects 报告半开区间 [from, to) 是否与任一保全相交（端点相切不算）。
func (r *Registry) Intersects(from, to int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.m {
		if h.From < to && from < h.To {
			return true
		}
	}
	return false
}

// Snapshot 返回按 id 排序的保全副本，供折叠循环在无锁下稳定判定。
func (r *Registry) Snapshot() []Interval {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.m))
	for id := range r.m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Interval, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.m[id])
	}
	return out
}

const maxTS = int64(10_000_000_000_000)
