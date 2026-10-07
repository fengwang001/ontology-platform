package validate

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registration 是一次批次操作中的单条注册声明。
// 同一批次内 Registration 的相对顺序决定组内执行顺序，
// 与批次整体提交时刻无关。
type Registration[T any] struct {
	Hook Hook[T]
}

// Snapshot 是某次校验调用开始时刻所读到的、不可变的钩子集合视图。
// 后续运行期发生的注册/注销对已发出的 Snapshot 没有任何影响。
type Snapshot[T any] struct {
	// Version 单调递增；每次成功的注册/注销/批次操作使其加一。
	Version uint64
	// Groups 按优先级（高→低，同优先级按名字）排序。
	Groups []SnapshotGroup[T]
}

// SnapshotGroup 是快照中的一个分组。
type SnapshotGroup[T any] struct {
	Name         string
	Priority     Priority
	ShortCircuit bool
	// Hooks 严格按注册先后（批次内按声明相对顺序）排列。
	Hooks []Hook[T]
}

// Registry 是对象类型或链接类型的校验钩子注册表。
// 零值不可用，必须使用 NewRegistry 构造。
type Registry[T any] struct {
	mu    sync.Mutex
	state *state[T]
}

// state 是注册表的不可变状态。任何变更都通过 copy-on-write 生成新 state，
// 再在互斥临界区内以单次赋值发布，因此一次注册/注销/批次在赋值点线性化，
// Validate/Snapshot 在其读锁临界区内的读点线性化，整体可串行化。
type state[T any] struct {
	version uint64
	// groups 按 group 名索引；groupOrder 按优先级排序。
	groups     map[string]GroupSpec
	groupOrder []string
	// hooks: group -> 组内有序钩子（只包含当前存活钩子，无墓碑）。
	hooks map[string][]Hook[T]
	// ids: hookID -> group，用于检查 ID 全局唯一。
	ids map[string]string
}

// NewRegistry 创建注册表并声明分组。重复分组名返回错误。
func NewRegistry[T any](groups ...GroupSpec) (*Registry[T], error) {
	r := &Registry[T]{}
	if err := r.declareInitial(groups); err != nil {
		return nil, err
	}
	return r, nil
}

// DeclareGroup 在运行期追加声明一个分组（动态扩展）。
func (r *Registry[T]) DeclareGroup(spec GroupSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("validate: group name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	if _, ok := s.groups[spec.Name]; ok {
		return fmt.Errorf("validate: group %q already declared", spec.Name)
	}
	next := s.clone()
	next.version = s.version + 1
	next.groups[spec.Name] = spec
	next.groupOrder = append(next.groupOrder, spec.Name)
	sortGroups(next)
	r.state = next
	return nil
}

// Register 注册单个钩子。重复 ID 或引用未声明分组时返回错误且不改变状态。
func (r *Registry[T]) Register(h Hook[T]) error {
	if err := h.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	if err := checkRegisterable(s, []Hook[T]{h}); err != nil {
		return err
	}
	next := s.clone()
	next.version = s.version + 1
	next.hooks[h.Group] = append(next.hooks[h.Group], h)
	next.ids[h.ID] = h.Group
	r.state = next
	return nil
}

// RegisterBatch 以单个对外动作的原子语义注册多条钩子；
// 任一条非法则整体不生效。组内顺序按批次内声明的相对顺序确定。
func (r *Registry[T]) RegisterBatch(regs ...Registration[T]) error {
	if len(regs) == 0 {
		return nil
	}
	hooks := make([]Hook[T], len(regs))
	for i, reg := range regs {
		if err := reg.Hook.validate(); err != nil {
			return err
		}
		hooks[i] = reg.Hook
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	if err := checkRegisterable(s, hooks); err != nil {
		return err
	}
	next := s.clone()
	next.version = s.version + 1
	// 关键取舍：按批次内声明顺序逐条 append 到其所属分组末尾，
	// 严格保留声明的相对顺序；所有条目在同一次状态发布中生效，
	// 不存在“按批次整体提交到系统的时刻先后重排”的问题。
	for _, h := range hooks {
		next.hooks[h.Group] = append(next.hooks[h.Group], h)
		next.ids[h.ID] = h.Group
	}
	r.state = next
	return nil
}

// Unregister 注销一个钩子。注销立即移除且不保留其在组内的历史位置；
// 之后同 ID 重新注册会作为全新注册排到分组当前末尾。
// 返回值表示注销前该钩子是否存在。
func (r *Registry[T]) Unregister(id string) (existed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	group, ok := s.ids[id]
	if !ok {
		return false
	}
	next := s.clone()
	next.version = s.version + 1
	old := s.hooks[group]
	updated := make([]Hook[T], 0, len(old)-1)
	for _, h := range old {
		if h.ID != id {
			updated = append(updated, h)
		}
	}
	// 直接重建切片：不保留墓碑，也不保留被注销钩子的位置信息。
	next.hooks[group] = updated
	delete(next.ids, id)
	r.state = next
	return true
}

// Snapshot 返回当前注册表的一个独立、不可变的值拷贝快照。
// 快照只包含当前存活的钩子；历史注册/注销总量不会在快照中留下任何数据。
func (r *Registry[T]) Snapshot() Snapshot[T] {
	r.mu.Lock()
	s := r.state
	r.mu.Unlock()
	return buildSnapshot(s)
}

// Validate 以调用开始时刻的快照执行全部适用钩子。
// 钩子自身异常时返回 *HookExecutionError，同时返回记录到异常点为止的 Report。
func (r *Registry[T]) Validate(ctx context.Context, target T) (*Report[T], error) {
	snap := r.Snapshot()
	return executeSnapshot(ctx, snap, target)
}

func (r *Registry[T]) declareInitial(groups []GroupSpec) error {
	if len(groups) == 0 {
		return fmt.Errorf("validate: at least one group must be declared")
	}
	seen := make(map[string]struct{}, len(groups))
	order := make([]string, 0, len(groups))
	specs := make(map[string]GroupSpec, len(groups))
	for _, g := range groups {
		if g.Name == "" {
			return fmt.Errorf("validate: group name must not be empty")
		}
		if _, dup := seen[g.Name]; dup {
			return fmt.Errorf("validate: duplicate group declaration %q", g.Name)
		}
		seen[g.Name] = struct{}{}
		specs[g.Name] = g
		order = append(order, g.Name)
	}
	s := &state[T]{
		groups:     specs,
		groupOrder: order,
		hooks:      make(map[string][]Hook[T]),
		ids:        make(map[string]string),
	}
	sortGroups(s)
	r.state = s
	return nil
}

func (s *state[T]) clone() *state[T] {
	next := &state[T]{
		version:    s.version,
		groups:     make(map[string]GroupSpec, len(s.groups)),
		groupOrder: append([]string(nil), s.groupOrder...),
		hooks:      make(map[string][]Hook[T], len(s.hooks)),
		ids:        make(map[string]string, len(s.ids)),
	}
	for name, spec := range s.groups {
		next.groups[name] = spec
	}
	for group, hs := range s.hooks {
		next.hooks[group] = append([]Hook[T](nil), hs...)
	}
	for id, group := range s.ids {
		next.ids[id] = group
	}
	return next
}

func sortGroups[T any](s *state[T]) {
	sort.SliceStable(s.groupOrder, func(i, j int) bool {
		gi := s.groups[s.groupOrder[i]]
		gj := s.groups[s.groupOrder[j]]
		if gi.Priority != gj.Priority {
			return gi.Priority > gj.Priority
		}
		return gi.Name < gj.Name
	})
}

func checkRegisterable[T any](s *state[T], hooks []Hook[T]) error {
	pending := make(map[string]struct{}, len(hooks))
	for _, h := range hooks {
		if _, ok := s.groups[h.Group]; !ok {
			return fmt.Errorf("validate: hook %q references undeclared group %q", h.ID, h.Group)
		}
		if _, dup := s.ids[h.ID]; dup {
			return fmt.Errorf("validate: hook id %q already registered", h.ID)
		}
		if _, dup := pending[h.ID]; dup {
			return fmt.Errorf("validate: hook id %q duplicated within batch", h.ID)
		}
		pending[h.ID] = struct{}{}
	}
	return nil
}

func buildSnapshot[T any](s *state[T]) Snapshot[T] {
	groups := make([]SnapshotGroup[T], 0, len(s.groupOrder))
	for _, name := range s.groupOrder {
		spec := s.groups[name]
		hooks := append([]Hook[T](nil), s.hooks[name]...)
		groups = append(groups, SnapshotGroup[T]{
			Name:         spec.Name,
			Priority:     spec.Priority,
			ShortCircuit: spec.ShortCircuit,
			Hooks:        hooks,
		})
	}
	return Snapshot[T]{Version: s.version, Groups: groups}
}
