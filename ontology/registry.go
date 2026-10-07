package ontology

import (
	"sort"
	"sync"
)

// Registry 是校验钩子的注册表，负责分组声明、钩子注册/注销与快照提取。
//
// 所有变更操作与快照读取都经过同一把互斥锁串行化，
// 因此全部操作的可观察效果等价于某个全序串行执行。
type Registry struct {
	mu      sync.Mutex
	version uint64
	seq     uint64
	groups  map[string]*groupState
}

type groupState struct {
	spec  GroupSpec
	hooks map[string]*hookState
	order []string
}

type hookState struct {
	id   string
	seq  uint64
	hook Hook
}

// NewRegistry 返回一个空的注册表。
func NewRegistry() *Registry {
	return &Registry{groups: make(map[string]*groupState)}
}

// DeclareGroup 声明一个优先级分组。重复声明同名分组返回 ErrGroupExists。
//
// 分组声明不改变钩子快照内容（快照只含有钩子的分组），
// 因此不推进注册表版本号。
func (r *Registry) DeclareGroup(spec GroupSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.groups[spec.Name]; ok {
		return ErrGroupExists
	}
	r.groups[spec.Name] = &groupState{
		spec:  spec,
		hooks: make(map[string]*hookState),
	}
	return nil
}

// Register 注册单个钩子，返回注册完成后的注册表版本号。
func (r *Registry) Register(reg HookRegistration) (uint64, error) {
	return r.RegisterBatch([]HookRegistration{reg})
}

// RegisterBatch 把同一批次的多项注册作为单次原子动作提交，
// 对外表现为单次动作：整批在一次临界区内生效，
// 组内顺序按批次内声明的相对顺序确定，
// 不会按批次提交时刻与其他批次重新交错。
// 批次内任意一项不合法则整批不生效。
func (r *Registry) RegisterBatch(regs []HookRegistration) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[[2]string]struct{}, len(regs))
	for _, reg := range regs {
		if reg.Group == "" || reg.ID == "" || reg.Hook == nil {
			return r.version, ErrInvalidRegistration
		}
		g, ok := r.groups[reg.Group]
		if !ok {
			return r.version, ErrGroupNotDeclared
		}
		key := [2]string{reg.Group, reg.ID}
		if _, dup := seen[key]; dup {
			return r.version, ErrHookExists
		}
		if _, dup := g.hooks[reg.ID]; dup {
			return r.version, ErrHookExists
		}
		seen[key] = struct{}{}
	}

	for _, reg := range regs {
		g := r.groups[reg.Group]
		r.seq++
		g.hooks[reg.ID] = &hookState{id: reg.ID, seq: r.seq, hook: reg.Hook}
		g.order = append(g.order, reg.ID)
	}
	if len(regs) > 0 {
		r.version++
	}
	return r.version, nil
}

// Unregister 注销指定分组内的钩子，返回注销完成后的注册表版本号。
//
// 注销会丢弃钩子原有的注册序号；之后以相同分组与标识重新注册时，
// 作为一次全新注册排到该分组当前末尾。
func (r *Registry) Unregister(group, id string) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.groups[group]
	if !ok {
		return r.version, ErrHookNotFound
	}
	if _, ok := g.hooks[id]; !ok {
		return r.version, ErrHookNotFound
	}
	delete(g.hooks, id)
	for i, hid := range g.order {
		if hid == id {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	r.version++
	return r.version, nil
}

// Snapshot 提取调用时刻的不可变钩子快照。
//
// 快照在临界区内完成深拷贝，提取后发生的注册/注销不影响快照内容；
// 同一次校验调用内多次读取该快照必然一致。
// 拷贝开销只与当前存活钩子数量相关，与历史注册/注销总次数无关。
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	groups := make([]*groupState, 0, len(r.groups))
	for _, g := range r.groups {
		if len(g.order) > 0 {
			groups = append(groups, g)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i].spec, groups[j].spec
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.Name < b.Name
	})

	snap := Snapshot{Version: r.version, Groups: make([]GroupSnapshot, 0, len(groups))}
	for _, g := range groups {
		gs := GroupSnapshot{Spec: g.spec, Hooks: make([]HookSnapshot, 0, len(g.order))}
		for _, id := range g.order {
			h := g.hooks[id]
			gs.Hooks = append(gs.Hooks, HookSnapshot{ID: h.id, Seq: h.seq, Hook: h.hook})
		}
		snap.Groups = append(snap.Groups, gs)
	}
	return snap
}
