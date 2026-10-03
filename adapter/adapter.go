// Package adapter 维护相邻版本适配步与版本属性（下线时刻、预览作用域）
// 的注册表。注册表是唯一的可变状态持有者，所有方法可并发调用；
// 每次成功的变更使注册表版本号加 1，被拒绝的变更不改变任何状态。
package adapter

import (
	"fmt"
	"sync"
)

// MaxTime 是下线时刻与当前时间的最大合法取值。
const MaxTime = int64(1_000_000_000_000_000)

// Kind 区分变更被拒绝的原因类别。
type Kind int

const (
	// KindInvalidArgument 表示参数越界（v、t、scope 非法）。
	KindInvalidArgument Kind = iota
	// KindNotFound 表示要删除的适配步未登记。
	KindNotFound
)

// Error 是一次被拒绝的变更。
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func invalidf(format string, args ...any) *Error {
	return &Error{Kind: KindInvalidArgument, Msg: "adapter: " + fmt.Sprintf(format, args...)}
}

// Step 是版本 v 与 v+1 之间的适配步。
type Step struct {
	// ReqLossy 为真表示请求由 v 升到 v+1 会丢失客户端信息。
	ReqLossy bool
	// RespLossy 为真表示响应由 v+1 降回 v 会丢失信息。
	RespLossy bool
}

// Snapshot 是注册表某一时刻的不可变视图，版本号与内容取自同一瞬间。
type Snapshot struct {
	H        int
	Steps    map[int]Step
	Sunsets  map[int]int64
	Previews map[int]string
	Version  uint64
}

// Registry 是头版本为 H 的适配注册表，只直接服务版本 H。
type Registry struct {
	mu       sync.RWMutex
	h        int
	steps    map[int]Step
	sunsets  map[int]int64
	previews map[int]string
	version  uint64
}

// New 创建头版本为 h 的注册表，h 须在 [1, 1000]。
func New(h int) (*Registry, error) {
	if h < 1 || h > 1000 {
		return nil, invalidf("head version %d out of range [1,1000]", h)
	}
	return &Registry{
		h:        h,
		steps:    make(map[int]Step),
		sunsets:  make(map[int]int64),
		previews: make(map[int]string),
	}, nil
}

// H 返回后端头版本。
func (r *Registry) H() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.h
}

// Version 返回当前注册表版本号（初值 0，每次成功变更加 1）。
func (r *Registry) Version() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// SetAdapter 登记或覆盖版本 v 与 v+1 之间的适配步，要求 1<=v<H。
func (r *Registry) SetAdapter(v int, reqLossy, respLossy bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v >= r.h {
		return invalidf("adapter step %d out of range [1,%d)", v, r.h)
	}
	r.steps[v] = Step{ReqLossy: reqLossy, RespLossy: respLossy}
	r.version++
	return nil
}

// RemoveAdapter 删除版本 v 与 v+1 之间的适配步；步未登记时报 KindNotFound。
func (r *Registry) RemoveAdapter(v int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v >= r.h {
		return invalidf("adapter step %d out of range [1,%d)", v, r.h)
	}
	if _, ok := r.steps[v]; !ok {
		return &Error{Kind: KindNotFound, Msg: fmt.Sprintf("adapter: step %d not registered", v)}
	}
	delete(r.steps, v)
	r.version++
	return nil
}

// Sunset 登记版本 v 自时刻 t 起下线（now>=t 即已下线），重复调用覆盖，
// 要求 1<=v<H 且 t 在 [0, MaxTime]。
func (r *Registry) Sunset(v int, t int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v >= r.h {
		return invalidf("sunset version %d out of range [1,%d)", v, r.h)
	}
	if t < 0 || t > MaxTime {
		return invalidf("sunset time %d out of range [0,%d]", t, MaxTime)
	}
	r.sunsets[v] = t
	r.version++
	return nil
}

// Preview 把版本 v 标为预览，使用它需要持有 scope，要求 1<=v<=H 且 scope 非空。
func (r *Registry) Preview(v int, scope string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v < 1 || v > r.h {
		return invalidf("preview version %d out of range [1,%d]", v, r.h)
	}
	if scope == "" {
		return invalidf("preview scope must not be empty")
	}
	r.previews[v] = scope
	r.version++
	return nil
}

// Snapshot 返回注册表当前内容的一次性深拷贝，协商据此看到原子视图。
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap := Snapshot{
		H:        r.h,
		Steps:    make(map[int]Step, len(r.steps)),
		Sunsets:  make(map[int]int64, len(r.sunsets)),
		Previews: make(map[int]string, len(r.previews)),
		Version:  r.version,
	}
	for v, s := range r.steps {
		snap.Steps[v] = s
	}
	for v, t := range r.sunsets {
		snap.Sunsets[v] = t
	}
	for v, sc := range r.previews {
		snap.Previews[v] = sc
	}
	return snap
}
