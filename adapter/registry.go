package adapter

import (
	"errors"
	"sync"
)

// Step 描述版本 v 与 v+1 之间的适配步。
type Step struct {
	ReqLossy  bool
	RespLossy bool
}

// Snapshot 是注册表某一版本号下的不可变快照。
type Snapshot struct {
	H       int
	Rev     int
	Steps   map[int]Step
	Sunset  map[int]int64
	Preview map[int]string
}

// Registry 是并发安全的版本属性注册表。
type Registry struct {
	mu      sync.RWMutex
	h       int
	rev     int
	steps   map[int]Step
	sunset  map[int]int64
	preview map[int]string
}

var (
	// ErrInvalidArgument 表示变更参数越界或为空。
	ErrInvalidArgument = errors.New("adapter: invalid argument")
	// ErrNotFound 表示要删除的适配步尚未登记。
	ErrNotFound = errors.New("adapter: adapter step not found")
)

const maxTime int64 = 1_000_000_000_000_000

// New 创建后端头版本为 h 的注册表。
func New(h int) (*Registry, error) {
	if h < 1 || h > 1000 {
		return nil, ErrInvalidArgument
	}
	return &Registry{
		h:       h,
		steps:   make(map[int]Step),
		sunset:  make(map[int]int64),
		preview: make(map[int]string),
	}, nil
}

// H 返回后端头版本。
func (r *Registry) H() int {
	return r.h
}

// SetAdapter 登记或覆盖适配步 v（连接版本 v 与 v+1），成功后版本号加 1。
func (r *Registry) SetAdapter(v int, reqLossy, respLossy bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v >= r.h {
		return ErrInvalidArgument
	}
	r.steps[v] = Step{ReqLossy: reqLossy, RespLossy: respLossy}
	r.rev++
	return nil
}

// RemoveAdapter 删除适配步 v；参数非法优先于步未登记，成功后版本号加 1。
func (r *Registry) RemoveAdapter(v int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v >= r.h {
		return ErrInvalidArgument
	}
	if _, ok := r.steps[v]; !ok {
		return ErrNotFound
	}
	delete(r.steps, v)
	r.rev++
	return nil
}

// Sunset 登记版本 v 自时刻 t 起下线（now >= t 视为已下线），成功后版本号加 1。
func (r *Registry) Sunset(v int, t int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v >= r.h || t < 0 || t > maxTime {
		return ErrInvalidArgument
	}
	r.sunset[v] = t
	r.rev++
	return nil
}

// Preview 把版本 v 标记为预览，使用它必须持有 scope；重复登记覆盖作用域，成功后版本号加 1。
func (r *Registry) Preview(v int, scope string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v > r.h || scope == "" {
		return ErrInvalidArgument
	}
	r.preview[v] = scope
	r.rev++
	return nil
}

// Revision 返回当前注册表版本号。
func (r *Registry) Revision() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rev
}

// SnapshotAt 取得当前注册表的不可变深拷贝，供一次协商全程使用。
func (r *Registry) SnapshotAt() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	steps := make(map[int]Step, len(r.steps))
	for k, v := range r.steps {
		steps[k] = v
	}
	sunset := make(map[int]int64, len(r.sunset))
	for k, v := range r.sunset {
		sunset[k] = v
	}
	preview := make(map[int]string, len(r.preview))
	for k, v := range r.preview {
		preview[k] = v
	}
	return Snapshot{
		H:       r.h,
		Rev:     r.rev,
		Steps:   steps,
		Sunset:  sunset,
		Preview: preview,
	}
}
