package subtype

import "sync"

// Registry 登记命名类型定义并判定类型表达式之间的子类型关系。
// 零值不可用，请用 NewRegistry 构造。并发安全。
type Registry struct {
	mu   sync.RWMutex
	defs map[string]*Type
}

func NewRegistry() *Registry {
	return &Registry{defs: map[string]*Type{}}
}

// Register 登记一个命名类型定义。允许前向引用与递归引用；
// 未定义引用与无保护循环不在登记时检查，而在判定时报告。
//
// 错误优先级：参数非法（空名字、定义内对象属性名重复等）先于重复定义。
// 被拒绝的登记不改变任何已登记状态。
func (r *Registry) Register(name string, def *Type) error {
	if name == "" {
		return invalidf("empty type name")
	}
	if err := validateExpr(def); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[name]; exists {
		return &Error{Kind: ErrDuplicateDefinition, Name: name}
	}
	r.defs[name] = def
	return nil
}

// Snapshot 返回当前已登记定义的一致快照（拷贝映射，类型本身不可变）。
func (r *Registry) Snapshot() map[string]*Type {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*Type, len(r.defs))
	for k, v := range r.defs {
		out[k] = v
	}
	return out
}

// Stats 记录一次判定的规模信息，用于验证复杂度界限。
type Stats struct {
	LeftNames       int // 左类型静态可达的命名类型数
	RightNames      int // 右类型静态可达的命名类型数
	LeftNodes       int // 左类型静态闭包中的节点数
	RightNodes      int // 右类型静态闭包中的节点数
	PairsDiscovered int // 判定实际考察的不同（左，右）对数
	NamedPairs      int // 其中不同（左名，右名）命名对数
	Iterations      int // 不动点迭代轮数
}

// Result 是一次判定的结果。
type Result struct {
	Subtype bool
	Stats   Stats
}

// Check 判定 s 是否为 t 的子类型，并返回规模统计。
//
// 错误优先级：参数非法 > 未定义引用 > 无保护循环。
// 未定义引用与无保护循环对两个待比较类型静态可达的全部命名定义检查，
// 各自报告字典序最小的名字。判定基于一致的定义快照，不修改任何已登记定义。
func (r *Registry) Check(s, t *Type) (Result, error) {
	if err := validateExpr(s); err != nil {
		return Result{}, err
	}
	if err := validateExpr(t); err != nil {
		return Result{}, err
	}
	defs := r.Snapshot()
	lNodes, lNames, lUndef := closure(defs, s)
	rNodes, rNames, rUndef := closure(defs, t)
	if u := minString(lUndef, rUndef); u != "" {
		return Result{}, &Error{Kind: ErrUndefinedReference, Name: u}
	}
	allNames := map[string]bool{}
	for n := range lNames {
		allNames[n] = true
	}
	for n := range rNames {
		allNames[n] = true
	}
	if n, ok := findUnguardedCycle(defs, allNames); ok {
		return Result{}, &Error{Kind: ErrUnguardedCycle, Name: n}
	}
	eng := newEngine(defs)
	sub, stats := eng.run(s, t)
	stats.LeftNames = len(lNames)
	stats.RightNames = len(rNames)
	stats.LeftNodes = len(lNodes)
	stats.RightNodes = len(rNodes)
	return Result{Subtype: sub, Stats: stats}, nil
}

// IsSubtype 是 Check 的简写：仅返回成立与否。
func (r *Registry) IsSubtype(s, t *Type) (bool, error) {
	res, err := r.Check(s, t)
	if err != nil {
		return false, err
	}
	return res.Subtype, nil
}

func minString(a, b []string) string {
	best := ""
	for _, s := range a {
		if best == "" || s < best {
			best = s
		}
	}
	for _, s := range b {
		if best == "" || s < best {
			best = s
		}
	}
	return best
}
