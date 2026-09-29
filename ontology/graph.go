package ontology

// View 描述一个物化视图的静态定义。
// 没有 Dependencies 的是基视图，其值由外部通过 SetBase 提供；
// 有 Dependencies 的是派生视图，其值只能由 Fn 依据依赖当前值推导。
type View struct {
	Name         string
	Dependencies []string
	// Fn 入参与 Dependencies 一一对应，返回视图的新值。
	Fn func(deps []any) (any, error)
}

// Graph 是物化视图依赖图。零值不可用，请用 NewGraph 构造。
type Graph struct {
	mu rwLock

	views      map[string]*viewState
	dependents map[string]map[string]struct{} // dep -> 直接依赖它的视图集合
	order      []string                       // 注册顺序，用于确定性的并列排序
	generation uint64
}

type viewState struct {
	spec  View
	index int // 注册序号

	value        any
	materialized bool
	dirty        bool
}

// NewGraph 创建空依赖图。
func NewGraph() *Graph {
	return &Graph{
		mu:         newWriterPriorityRWMutex(),
		views:      make(map[string]*viewState),
		dependents: make(map[string]map[string]struct{}),
	}
}

// Register 注册一个视图定义，允许前向引用其依赖。
func (g *Graph) Register(v View) error {
	if v.Name == "" {
		return graphError(ErrEmptyName, "", "view name must not be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.views[v.Name]; exists {
		return graphError(ErrDuplicateName, v.Name, "a view with this name is already registered")
	}
	if len(v.Dependencies) == 0 {
		if v.Fn != nil {
			return graphError(ErrBaseViewWithFn, v.Name, "base views (no dependencies) cannot carry a compute function")
		}
	} else if v.Fn == nil {
		return graphError(ErrDerivedWithoutFn, v.Name, "derived views require a compute function")
	}

	if cycle := g.findCycleWithCandidate(v); cycle != "" {
		return graphError(ErrCycleDetected, cycle, "registering this view would form a dependency cycle")
	}

	g.views[v.Name] = &viewState{
		spec:  v,
		index: len(g.order),
		dirty: true, // 新视图尚未物化，等待首次重算
	}
	for _, dep := range v.Dependencies {
		if g.dependents[dep] == nil {
			g.dependents[dep] = make(map[string]struct{})
		}
		g.dependents[dep][v.Name] = struct{}{}
	}
	g.order = append(g.order, v.Name)
	return nil
}

// findCycleWithCandidate 在“假定候选视图已注册”的图上做 DFS 环检测，
// 尚未注册的依赖名（前向引用）暂视为没有出边。
// 返回环上的一个视图名；无环时返回空串。
func (g *Graph) findCycleWithCandidate(candidate View) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.views)+1)

	depsOf := func(name string) []string {
		if name == candidate.Name {
			return candidate.Dependencies
		}
		if vs, ok := g.views[name]; ok {
			return vs.spec.Dependencies
		}
		return nil
	}

	var dfs func(name string) string
	dfs = func(name string) string {
		color[name] = gray
		for _, dep := range depsOf(name) {
			if dep != candidate.Name {
				if _, known := g.views[dep]; !known {
					continue // 前向引用：依赖尚未注册，不参与本次环检测
				}
			}
			switch color[dep] {
			case gray:
				return dep
			case white:
				if cyc := dfs(dep); cyc != "" {
					return cyc
				}
			}
		}
		color[name] = black
		return ""
	}

	if cyc := dfs(candidate.Name); cyc != "" {
		return cyc
	}
	for _, name := range g.order {
		if color[name] == white {
			if cyc := dfs(name); cyc != "" {
				return cyc
			}
		}
	}
	return ""
}

// markDownstreamDirty 把起点及其全部下游传递闭包标记为脏。
// 调用方必须持有写锁。
func (g *Graph) markDownstreamDirty(roots ...string) {
	queue := append([]string(nil), roots...)
	seen := make(map[string]bool, len(roots))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if vs, ok := g.views[cur]; ok {
			vs.dirty = true
		}
		for down := range g.dependents[cur] {
			if !seen[down] {
				queue = append(queue, down)
			}
		}
	}
}
