package constexpr

import "sync"

// entry 是登记后固定下来的常量值。
type entry struct {
	value *Value
}

// snapshot 是某一时刻命名常量表的不可变视图；map 一经发布不再修改，
// 因此求值期间无需持锁即可 O(1) 读取。
type snapshot struct {
	names map[string]*entry
}

func emptySnapshot() snapshot { return snapshot{names: map[string]*entry{}} }

func (s snapshot) lookup(name string) (*Value, bool) {
	e, ok := s.names[name]
	if !ok {
		return nil, false
	}
	return e.value, true
}

// Registry 是并发安全的命名常量表。
type Registry struct {
	mu   sync.RWMutex
	snap snapshot
}

// NewRegistry 创建空表。
func NewRegistry() *Registry { return &Registry{} }

// Register 登记无类型命名常量（值在登记时求定）。
//
// 表达式若需要具体类型语境，可在内部使用 OpDefaultConv；登记本身不强制类型。
// 被拒绝的登记不改变任何状态。
func (r *Registry) Register(name string, e *Expr) error {
	return r.register(name, e, CTypeInvalid)
}

// RegisterTyped 登记带显式类型的命名常量。
func (r *Registry) RegisterTyped(name string, typeName string, e *Expr) error {
	t, ok := ParseCType(typeName)
	if !ok {
		return errf(EvalInvalidArgument, "未知类型名 %q", typeName)
	}
	return r.register(name, e, t)
}

func (r *Registry) register(name string, e *Expr, t CType) error {
	// 参数非法（空名字、结构非法）优先于重复登记。
	if name == "" {
		return errf(EvalInvalidArgument, "命名常量的名字为空")
	}
	if err := validateStructure(e); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, dup := r.snap.names[name]; dup {
		return errf(EvalDuplicateName, "命名常量 %q 重复登记", name)
	}

	// 在当前快照上求值：未知名字优先于求值错误。
	ev := &Evaluator{snap: r.snap}
	v, err := ev.Eval(e)
	if err != nil {
		return err
	}
	if t != CTypeInvalid {
		v, err = convertValue(v, t)
		if err != nil {
			return err
		}
	}

	// 一切成功后才复制并发布新快照（写时复制）。
	next := snapshot{names: make(map[string]*entry, len(r.snap.names)+1)}
	for k, ent := range r.snap.names {
		next.names[k] = ent
	}
	next.names[name] = &entry{value: v}
	r.snap = next
	return nil
}

// Lookup 返回命名常量值的拷贝；不存在时第二返回值为 false（O(1)）。
func (r *Registry) Lookup(name string) (*Value, bool) {
	r.mu.RLock()
	snap := r.snap
	r.mu.RUnlock()
	e, ok := snap.lookup(name)
	if !ok {
		return nil, false
	}
	return e.Clone(), true
}

// Len 返回已登记常量数量。
func (r *Registry) Len() int {
	r.mu.RLock()
	n := len(r.snap.names)
	r.mu.RUnlock()
	return n
}

// NewEvaluator 在当前快照上创建求值器。
func (r *Registry) NewEvaluator(opts ...Option) *Evaluator {
	r.mu.RLock()
	snap := r.snap
	r.mu.RUnlock()
	ev := &Evaluator{snap: snap}
	for _, o := range opts {
		o(ev)
	}
	return ev
}
