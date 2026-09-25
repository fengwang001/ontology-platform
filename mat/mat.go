// Package mat 在 dep.Graph 之上提供延迟物化：惰性求值、缓存、传递失效。
package mat

import (
	"errors"
	"sync"

	"ontology/dep"
)

// 运行期可判定的哨兵错误。
var (
	// ErrUnknownColumn 表示 Get/Set 了一个未声明的列。
	ErrUnknownColumn = errors.New("mat: unknown column")
	// ErrSetDerived 表示对派生列调用了 Set。
	ErrSetDerived = errors.New("mat: cannot Set a derived column")
)

// Engine 是单实例延迟物化引擎，状态全部驻留进程内存。
type Engine struct {
	g *dep.Graph

	mu sync.Mutex
	// bases：基列当前值；cache/valid：派生列缓存与有效性。
	bases map[string]int
	cache map[string]int
	valid map[string]bool

	// lastGetCalls 记录最近一次 Get 实际调用 Fn 的次数（非导出）。
	lastGetCalls int
}

// New 基于已校验的依赖图创建引擎；只装入基列初值，不计算任何派生列。
func New(g *dep.Graph) *Engine {
	e := &Engine{
		g:     g,
		bases: map[string]int{},
		cache: map[string]int{},
		valid: map[string]bool{},
	}
	for _, n := range g.Names() {
		if g.IsDerived(n) {
			e.valid[n] = false
		} else {
			e.bases[n] = g.Initial(n)
		}
	}
	return e
}

// Get 返回列当前值；派生列按需计算并缓存。未知列报错且不改状态。
func (e *Engine) Get(name string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.g.Has(name) {
		return 0, ErrUnknownColumn
	}
	e.lastGetCalls = 0
	return e.getLocked(name), nil
}

// getLocked 递归求值：基列直接返回；派生列命中有效缓存即返回，
// 否则先递归取全部依赖（依赖可能此刻才被计算），再调用 Fn 并缓存。
func (e *Engine) getLocked(name string) int {
	if !e.g.IsDerived(name) {
		return e.bases[name]
	}
	if e.valid[name] {
		return e.cache[name]
	}
	deps := e.g.Deps(name)
	vals := make([]int, len(deps))
	for i, d := range deps {
		vals[i] = e.getLocked(d)
	}
	v := e.g.Fn(name)(vals)
	e.cache[name] = v
	e.valid[name] = true
	e.lastGetCalls++
	return v
}

// Set 更新基列并失效其全部（传递）依赖派生列。
// 未知列或对派生列 Set 时在改任何状态前返回错误。
func (e *Engine) Set(name string, val int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.g.Has(name) {
		return ErrUnknownColumn
	}
	if e.g.IsDerived(name) {
		return ErrSetDerived
	}
	e.bases[name] = val
	for _, d := range e.g.DependentsClosure(name) {
		e.valid[d] = false
	}
	return nil
}
