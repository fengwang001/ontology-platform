// Package mat 实现惰性物化：派生列按需计算、缓存、按传递闭包失效。
package mat

import (
	"errors"
	"sync"

	"ontology/dep"
)

var (
	// ErrUnknownColumn Get/Set 了未声明的列。
	ErrUnknownColumn = errors.New("mat: unknown column")
	// ErrSetDerived 对派生列调用 Set。
	ErrSetDerived = errors.New("mat: cannot set derived column")
)

// Engine 惰性物化引擎，状态全在进程内存。
type Engine struct {
	mu     sync.Mutex
	base   map[string]int
	derive map[string]dep.Derived
	rev    map[string][]string // 反向邻接表
	cache  map[string]int
	valid  map[string]bool
	lastFn int // 最近一次 Get 实际调用 Fn 的次数（非导出，不进公开接口）
}

// NewEngine 校验 spec 后构建引擎；不计算任何派生列。
func NewEngine(s dep.Spec) (*Engine, error) {
	if err := dep.Validate(s); err != nil {
		return nil, err
	}
	base := make(map[string]int, len(s.Base))
	for k, v := range s.Base {
		base[k] = v
	}
	return &Engine{
		base:   base,
		derive: s.Derived,
		rev:    dep.Dependents(s),
		cache:  map[string]int{},
		valid:  map[string]bool{},
	}, nil
}

// Get 返回列当前值：基列直接返回；派生列缓存有效则返回缓存，
// 否则递归求依赖后用 Fn 计算并缓存。
func (e *Engine) Get(name string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.base[name]; !ok {
		if _, ok := e.derive[name]; !ok {
			return 0, ErrUnknownColumn
		}
	}
	e.lastFn = 0
	return e.get(name), nil
}

// get 必须在持锁状态下调用。
func (e *Engine) get(name string) int {
	if v, ok := e.base[name]; ok {
		return v
	}
	if e.valid[name] {
		return e.cache[name]
	}
	d := e.derive[name]
	args := make([]int, len(d.Deps))
	for i, dn := range d.Deps {
		args[i] = e.get(dn)
	}
	v := d.Fn(args)
	e.lastFn++
	e.cache[name] = v
	e.valid[name] = true
	return v
}

// Set 更新基列并使所有传递依赖它的派生列缓存失效。
// 所有校验先于任何状态写，失败不留痕。
func (e *Engine) Set(name string, val int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.derive[name]; ok {
		return ErrSetDerived
	}
	if _, ok := e.base[name]; !ok {
		return ErrUnknownColumn
	}
	e.base[name] = val
	for n := range dep.Closure(e.rev, name) {
		delete(e.valid, n)
		delete(e.cache, n)
	}
	return nil
}
