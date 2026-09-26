// Package reg 维护「名字 -> G 计数器」的注册表，内部加锁，可并发使用。
package reg

import (
	"errors"
	"sync"

	"ontology/gc"
)

// 三类可判定错误，互不相同（负条目错误复用 gc.ErrNegativeEntry）。
var (
	ErrNonPositiveIncrement = errors.New("reg: increment must be positive")
	ErrNegativeNode         = errors.New("reg: negative node id")
	ErrUnknownCounter       = errors.New("reg: unknown counter")
)

// Registry 是名字到 G 计数器的注册表。
type Registry struct {
	mu sync.RWMutex
	m  map[string]gc.Counter
}

// New 返回空注册表。
func New() *Registry { return &Registry{m: make(map[string]gc.Counter)} }

// Set 以 name 注册计数器 c 的副本；含负条目则整体拒绝、不留痕。
func (r *Registry) Set(name string, c gc.Counter) error {
	for _, cnt := range c {
		if cnt < 0 {
			return gc.ErrNegativeEntry
		}
	}
	cp := make(gc.Counter, len(c))
	for k, v := range c {
		cp[k] = v
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[name] = cp
	return nil
}

// Inc 把 name 中 node 的条目加 k。k<=0 或 node<0 时拒绝且状态不变。
func (r *Registry) Inc(name string, node, k int) error {
	if k <= 0 {
		return ErrNonPositiveIncrement
	}
	if node < 0 {
		return ErrNegativeNode
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.m[name]
	if !ok {
		return ErrUnknownCounter
	}
	c[node] += k
	return nil
}

// MergeInto 把 nameA 更新为 nameA 与 nameB 逐条目取 max，返回 nameA 新值。
// 任一计数器不存在或含负条目时整体失败、状态不变。
func (r *Registry) MergeInto(nameA, nameB string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.m[nameA]
	if !ok {
		return 0, ErrUnknownCounter
	}
	b, ok := r.m[nameB]
	if !ok {
		return 0, ErrUnknownCounter
	}
	merged, err := gc.Merge(a, b)
	if err != nil {
		return 0, err
	}
	r.m[nameA] = merged
	return gc.Value(merged), nil
}

// Value 返回 name 的所有条目之和。
func (r *Registry) Value(name string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[name]
	if !ok {
		return 0, ErrUnknownCounter
	}
	return gc.Value(c), nil
}

// Snapshot 返回 name 计数器的副本，供展示与测试观察内容。
func (r *Registry) Snapshot(name string) (gc.Counter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[name]
	if !ok {
		return nil, ErrUnknownCounter
	}
	cp := make(gc.Counter, len(c))
	for k, v := range c {
		cp[k] = v
	}
	return cp, nil
}
