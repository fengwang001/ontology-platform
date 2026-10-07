// Package ontology：本文件负责按阶段注册钩子并解析一次转移的触发范围。
package ontology

import "sync"

// CommitSemantic 描述钩子记录副作用在转移失败时的提交语义。
type CommitSemantic string

const (
	// CommitOnSuccess 钩子副作用随转移一起提交；转移失败时执行其补偿动作撤销。
	CommitOnSuccess CommitSemantic = "commit_on_success"
	// CommitImmediately 钩子副作用一经产生即落定，不随转移失败而撤销。
	CommitImmediately CommitSemantic = "commit_immediately"
)

// TransitionContext 是钩子执行时可见的转移现场（只读快照）。
type TransitionContext struct {
	InstanceID string
	From       string
	To         string
}

// Hook 是一个已注册的校验钩子。
type Hook struct {
	ID         string
	Semantic   CommitSemantic
	Check      func(*TransitionContext) error
	Compensate func(*TransitionContext)
}

// edgeKey 是「具体转移钩子」注册表的键：起始阶段 -> 目标阶段。
type edgeKey struct{ from, to string }

// Probe 统计一次触发范围解析过程中触碰的注册表单元数，用于复杂度可验证证明。
type Probe struct{ CellsTouched int }

// HookRegistry 按对象类型维护两类钩子的索引。
type HookRegistry struct {
	mu sync.RWMutex
	// transitions: 具体转移钩子，按 (from,to) 精确键索引；自转移与普通转移同构。
	transitions map[edgeKey][]Hook
	// entries: 「进入 stage」钩子，按目标阶段索引。
	entries map[string][]Hook
	// counts 分别记录两类钩子的注册总数，仅用于复杂度证明与自检。
	transitionCount int
	entryCount      int
}

// NewHookRegistry 创建空的钩子注册表。
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		transitions: make(map[edgeKey][]Hook),
		entries:     make(map[string][]Hook),
	}
}

// RegisterTransition 注册「from -> to 这一具体转移」的钩子（允许 from == to）。
// 同一目标上的多个钩子按注册先后顺序触发。
func (r *HookRegistry) RegisterTransition(from, to string, h Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := edgeKey{from: from, to: to}
	r.transitions[key] = append(r.transitions[key], h)
	r.transitionCount++
}

// RegisterEntry 注册「进入 stage」钩子（不论起始阶段为何都触发）。
func (r *HookRegistry) RegisterEntry(stage string, h Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[stage] = append(r.entries[stage], h)
	r.entryCount++
}

// Resolve 解析一次转移按触发顺序排列的钩子：具体转移钩子在前，进入钩子在后。
// 解析成本只与两次哈希查表 + 实际命中数相关，与全部转移关系/全部钩子总数无关。
func (r *HookRegistry) Resolve(from, to string) []Hook {
	hooks, _ := r.resolve(from, to, nil)
	return hooks
}

// ResolveProbed 与 Resolve 等价，同时通过 Probe 回报本次解析触碰的注册表单元数。
// 单元数恒为「两次哈希键查找 + 两个命中切片的逐元素拷贝」，
// 因此随注册表规模增长保持常数（命中数不变时），是复杂度性质的可验证观测点。
func (r *HookRegistry) ResolveProbed(from, to string) ([]Hook, Probe) {
	var probe Probe
	hooks, p := r.resolve(from, to, &probe)
	return hooks, p
}

// TransitionHookCount / EntryHookCount 回报注册表规模，供测试做规模化对比。
func (r *HookRegistry) TransitionHookCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transitionCount
}

func (r *HookRegistry) EntryHookCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entryCount
}

func (r *HookRegistry) resolve(from, to string, probe *Probe) ([]Hook, Probe) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	edgeHooks := r.transitions[edgeKey{from: from, to: to}] // 哈希键查找 1 次
	entryHooks := r.entries[to]                             // 哈希键查找 1 次

	result := make([]Hook, 0, len(edgeHooks)+len(entryHooks))
	result = append(result, edgeHooks...) // 仅遍历实际命中的钩子
	result = append(result, entryHooks...)

	var p Probe
	p.CellsTouched = 2 + len(edgeHooks) + len(entryHooks)
	if probe != nil {
		*probe = p
	}
	return result, p
}
