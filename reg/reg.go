// Package reg 是副本名字 -> 版本向量的注册表，负责名字查找与合法性校验。
// 依赖 vv；不允许被 vv 反向依赖。
package reg

import (
	"errors"
	"sync"

	"ontology/vv"
)

// 三类互不相同的哨兵错误。
var (
	ErrNegativeCounter = errors.New("reg: vector contains negative counter")
	ErrNegativeActor   = errors.New("reg: vector contains negative actor id")
	ErrUnknownName     = errors.New("reg: replica name is not registered")
)

// Registry 保存各副本当前的版本向量。
type Registry struct {
	mu sync.RWMutex
	m  map[string]vv.Vector
}

// New 创建空注册表。
func New() *Registry {
	return &Registry{m: make(map[string]vv.Vector)}
}

// Set 注册/更新 name 的向量；含负值时整体失败且不改状态。
// 先完整校验、再一次性写入，保证失败不留痕。
func (r *Registry) Set(name string, v vv.Vector) error {
	for actor, counter := range v {
		if actor < 0 {
			return ErrNegativeActor
		}
		if counter < 0 {
			return ErrNegativeCounter
		}
	}
	cp := make(vv.Vector, len(v))
	for k, c := range v {
		cp[k] = c
	}
	r.mu.Lock()
	r.m[name] = cp
	r.mu.Unlock()
	return nil
}

// Merge 对两个已注册副本的向量做 join，纯函数语义，不改注册表与输入。
func (r *Registry) Merge(nameA, nameB string) (vv.Vector, error) {
	a, b, err := r.lookup2(nameA, nameB)
	if err != nil {
		return nil, err
	}
	return vv.Merge(a, b), nil
}

// Compare 比较两个已注册副本向量的因果关系。
func (r *Registry) Compare(nameA, nameB string) (vv.Order, error) {
	a, b, err := r.lookup2(nameA, nameB)
	if err != nil {
		return vv.Equal, err
	}
	return vv.Compare(a, b), nil
}

// lookup2 在持读锁下取出两个名字的向量；任一未注册即整体失败。
func (r *Registry) lookup2(nameA, nameB string) (vv.Vector, vv.Vector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.m[nameA]
	if !ok {
		return nil, nil, ErrUnknownName
	}
	b, ok := r.m[nameB]
	if !ok {
		return nil, nil, ErrUnknownName
	}
	return a, b, nil
}
