package ontology

import (
	"fmt"
	"sync"
)

// Definition 以纯数据方式一次性登记整张图：基底名集合与视图的直接依赖。
// 任一字段非法都会被 New 整体拒绝，且不产生任何实例。
type Definition struct {
	Bases []string            // 原始数值（叶子）名
	Views map[string][]string // 视图名 -> 直接依赖名（基底或其他视图）
}

// ChangeEvent 是变更日志中的一条原子记录。
type ChangeEvent struct {
	Batch int
	Kind  string // "retract" | "assert"
	Name  string
	Value float64
}

// ViewChange 把单个视图在一次刷新中的撤回/建立配对呈现。
type ViewChange struct {
	Batch  int
	Name   string
	Old    float64
	New    float64
	Events []ChangeEvent // 长度恒为 2：先 retract 后 assert
}

// Snapshot 是某一时刻逐字段一致的只读全量状态。
type Snapshot struct {
	Bases map[string]float64
	Views map[string]float64
	Dirty map[string]bool
	Log   []ChangeEvent
}

// Registry 是并发安全的视图依赖 DAG 增量刷新器。
type Registry struct {
	mu      sync.RWMutex
	g       *graph
	bases   map[string]float64 // 已生效基底值
	views   map[string]float64 // 已生效视图值（最近一次刷新后）
	pending map[string]float64 // 本批已登记未刷新的基底设值（同名末值）
	dirty   map[string]bool    // 待重算视图集合
	log     []ChangeEvent      // 累计变更日志（仅视图）
	batch   int                // 已完成的刷新批次数
}

// New 校验并构建注册器。任何错误都整体拒绝。
func New(def Definition, maxViews int) (*Registry, error) {
	g, err := buildGraph(def, maxViews)
	if err != nil {
		return nil, err
	}
	r := &Registry{
		g:       g,
		bases:   make(map[string]float64, len(g.baseNames)),
		views:   make(map[string]float64, len(g.viewNames)),
		pending: make(map[string]float64),
		dirty:   make(map[string]bool),
	}
	// 初始全量为 0：基底缺省为 0，沿拓扑序自底向上求和，所有视图亦为 0。
	for _, b := range g.baseNames {
		r.bases[b] = 0
	}
	for _, v := range g.topo {
		r.views[v] = r.sumDeps(v)
	}
	return r, nil
}

// sumDeps 按去重后直接依赖的字典序读取当前值求和，保证逐比特可复现。
// 调用方需自行持锁。
func (r *Registry) sumDeps(view string) float64 {
	var sum float64
	for _, dep := range r.g.deps[view] {
		sum += r.nodeValue(dep)
	}
	return sum
}

// nodeValue 读取某个基底或视图的当前生效值。调用方需自行持锁。
func (r *Registry) nodeValue(name string) float64 {
	if v, ok := r.bases[name]; ok {
		return v
	}
	return r.views[name]
}

// SetBase 登记一次基底设值并标脏传递闭包；失败不改任何状态。
func (r *Registry) SetBase(name string, value float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.isBase(name) {
		return fmt.Errorf("%w: %q", ErrUnknownBase, name)
	}
	r.pending[name] = value
	r.markDirty(name)
	return nil
}

// SetBases 批量登记设值；同批同名只最后一次生效。整体成功或整体失败。
func (r *Registry) SetBases(changes map[string]float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 先做全部校验：任一基底名未知就整体拒绝，脏集/日志/已登记变更保持不变。
	for name := range changes {
		if !r.isBase(name) {
			return fmt.Errorf("%w: %q", ErrUnknownBase, name)
		}
	}
	// 再统一落盘：map 同名键天然只保留最后一次赋值。
	for name, value := range changes {
		r.pending[name] = value
		r.markDirty(name)
	}
	return nil
}

// Refresh 把本批累计变更一次处理完，返回每个脏视图恰好一次的配对变更。
func (r *Registry) Refresh() []ViewChange {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.batch++
	batch := r.batch

	// 1) 本批基底设值全部生效：同名多次设值只保留最后一次（pending 已是末值）。
	for name, value := range r.pending {
		r.bases[name] = value
	}
	r.pending = make(map[string]float64)

	if len(r.dirty) == 0 {
		return nil
	}

	// 2) 沿全局拓扑序重算：任何视图必排在其全部依赖之后，每个脏视图恰好一次。
	changes := make([]ViewChange, 0, len(r.dirty))
	for _, v := range r.g.topo {
		if !r.dirty[v] {
			continue
		}
		old := r.views[v]
		retract := ChangeEvent{Batch: batch, Kind: "retract", Name: v, Value: old}

		next := r.sumDeps(v)
		assert := ChangeEvent{Batch: batch, Kind: "assert", Name: v, Value: next}

		// 3) 先输出撤回旧值，再输出建立新值；即便新旧相等也记录一次。
		r.log = append(r.log, retract, assert)
		r.views[v] = next
		changes = append(changes, ViewChange{
			Batch:  batch,
			Name:   v,
			Old:    old,
			New:    next,
			Events: []ChangeEvent{retract, assert},
		})
		delete(r.dirty, v)
	}
	return changes
}

// Value 查询单个视图（或基底）当前已生效值。
func (r *Registry) Value(name string) (float64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.known(name) {
		return 0, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return r.nodeValue(name), nil
}

// Snapshot 返回逐字段一致的全量状态副本。
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.SnapshotLocked()
}

// SnapshotLocked 在调用方已持有读锁的前提下构建快照，供需要跨多次读取保持
// 同一临界区一致性的场景使用。
func (r *Registry) SnapshotLocked() Snapshot {
	snap := Snapshot{
		Bases: make(map[string]float64, len(r.bases)),
		Views: make(map[string]float64, len(r.views)),
		Dirty: make(map[string]bool, len(r.dirty)),
		Log:   append([]ChangeEvent(nil), r.log...),
	}
	for k, v := range r.bases {
		snap.Bases[k] = v
	}
	for k, v := range r.views {
		snap.Views[k] = v
	}
	for k := range r.dirty {
		snap.Dirty[k] = true
	}
	return snap
}

// ChangeLog 返回累计变更日志副本。
func (r *Registry) ChangeLog() []ChangeEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ChangeEvent(nil), r.log...)
}

// Dirty 返回当前脏视图集合副本。
func (r *Registry) Dirty() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool, len(r.dirty))
	for k := range r.dirty {
		out[k] = true
	}
	return out
}

// SelfCheck 用定义复算所有视图并与当前状态核对，返回首个不一致描述。
func (r *Registry) SelfCheck() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.selfCheckLocked()
}

// selfCheckLocked 在调用方已持有读锁的前提下执行自检。
func (r *Registry) selfCheckLocked() error {
	// 待刷新状态下脏视图天然“暂时不一致”，自检核对的是已生效视图是否始终等于
	// 直接依赖当前值之和，且图结构不变量成立。
	for _, v := range r.g.viewNames {
		if r.dirty[v] {
			continue
		}
		got := r.views[v]
		want := r.sumDeps(v)
		if got != want {
			return fmt.Errorf("ontology: view %q = %v, want sum of deps %v", v, got, want)
		}
	}
	return nil
}

// RecomputeAll 自底向上全量重算所有视图，供本地核对。
func (r *Registry) RecomputeAll() (map[string]float64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bases := make(map[string]float64, len(r.bases))
	for k, v := range r.bases {
		bases[k] = v
	}
	for k, v := range r.pending { // 全量重算以“登记后的当前基底值”为基准
		bases[k] = v
	}
	values := make(map[string]float64, len(r.g.viewNames))
	get := func(name string) float64 {
		if v, ok := bases[name]; ok {
			return v
		}
		return values[name]
	}
	for _, v := range r.g.topo {
		var sum float64
		for _, dep := range r.g.deps[v] {
			sum += get(dep)
		}
		values[v] = sum
	}
	return values, nil
}

// isBase 判断名称是否为已登记基底。调用方需自行持锁。
func (r *Registry) isBase(name string) bool {
	_, ok := r.bases[name]
	return ok
}

// known 判断名称是否为已登记基底或视图。调用方需自行持锁。
func (r *Registry) known(name string) bool {
	if r.isBase(name) {
		return true
	}
	_, ok := r.views[name]
	return ok
}

// markDirty 沿反向边标记传递闭包（含可达即重复置位，集合天然去重）。
// 调用方需自行持写锁。
func (r *Registry) markDirty(changed string) {
	stack := append([]string(nil), r.g.dependents[changed]...)
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if r.dirty[v] {
			continue
		}
		r.dirty[v] = true
		stack = append(stack, r.g.dependents[v]...)
	}
}
