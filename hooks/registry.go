package hooks

import (
	"sync"
	"sync/atomic"
)

// transitionKey 是具体转移 (from -> to) 的索引键。
type transitionKey struct {
	from Phase
	to   Phase
}

// Registry 按触发范围登记并解析钩子。
//
// 内部使用两张哈希表：byTransition 以 (from, to) 为键，
// byEnter 以目标阶段为键。Resolve 只做两次哈希查找，
// 因此解析开销与注册表中的转移关系总数、钩子总数无关。
type Registry struct {
	mu           sync.RWMutex
	byTransition map[transitionKey][]Hook
	byEnter      map[Phase][]Hook

	// scanned 累计 Resolve 实际命中并返回的钩子条目数，
	// 用于以可验证的方式证明解析开销只与命中数相关。
	scanned atomic.Int64
}

// NewRegistry 构造一个空注册表。
func NewRegistry() *Registry {
	return &Registry{
		byTransition: make(map[transitionKey][]Hook),
		byEnter:      make(map[Phase][]Hook),
	}
}

// OnTransition 把钩子注册到具体转移 (from -> to) 上。
// 同一转移可注册多个钩子，按注册顺序触发。
//
// 注意：自转移 (p -> p) 的钩子只有显式注册到该具体转移上才会
// 触发；注册表不因 from == to 做任何默认跳过或默认触发。
func (r *Registry) OnTransition(from, to Phase, h Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := transitionKey{from: from, to: to}
	r.byTransition[k] = append(r.byTransition[k], h)
}

// OnEnter 把钩子注册到「进入阶段 phase」的触发范围上，
// 不论从哪个起始阶段转入（含自转移）都会触发。
func (r *Registry) OnEnter(phase Phase, h Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byEnter[phase] = append(r.byEnter[phase], h)
}

// Resolve 解析一次转移 (from -> to) 命中的钩子：
// transitionHooks 为该具体转移上注册的钩子（注册顺序），
// enterHooks 为目标阶段进入钩子（注册顺序）。
// 返回的切片是拷贝，调用方可安全持有；开销为 O(命中数)。
func (r *Registry) Resolve(from, to Phase) (transitionHooks, enterHooks []Hook) {
	r.mu.RLock()
	t := r.byTransition[transitionKey{from: from, to: to}]
	e := r.byEnter[to]
	// 拷贝命中条目，避免调用方与并发注册共享底层数组。
	transitionHooks = append([]Hook(nil), t...)
	enterHooks = append([]Hook(nil), e...)
	r.mu.RUnlock()

	r.scanned.Add(int64(len(transitionHooks) + len(enterHooks)))
	return transitionHooks, enterHooks
}

// Scanned 返回 Resolve 累计命中并返回的钩子条目数。
// 对同一次转移，该增量恒等于命中钩子数，与注册表规模无关。
func (r *Registry) Scanned() int64 { return r.scanned.Load() }

// Registered 返回已注册钩子总数，仅供测试与诊断使用。
func (r *Registry) Registered() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, hs := range r.byTransition {
		n += len(hs)
	}
	for _, hs := range r.byEnter {
		n += len(hs)
	}
	return n
}
