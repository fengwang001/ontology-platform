package ontology

// HookKind 区分两类钩子。
type HookKind int

const (
	// HookTransition 注册在具体转移 (from,to) 上，仅该转移触发。
	HookTransition HookKind = iota
	// HookEnter 注册在阶段进入点上，任何转入该阶段的转移都触发（含自转移）。
	HookEnter
)

func (k HookKind) String() string {
	if k == HookTransition {
		return "transition"
	}
	return "enter"
}

// CommitSemantics 声明钩子已产生的记录副作用在转移回退时的提交语义。
type CommitSemantics int

const (
	// RollbackOnFailure 转移失败时撤销该钩子的记录副作用。
	RollbackOnFailure CommitSemantics = iota
	// CommitOnFailure 转移失败时保留该钩子的记录副作用（如审计日志）。
	CommitOnFailure
)

// HookContext 是钩子执行时的上下文。
type HookContext struct {
	ObjectType string
	InstanceID string
	From       Stage
	To         Stage
	emit       func(rec SideEffect)
}

// Emit 记录一条钩子副作用（如校验日志），其是否随转移回退由钩子的提交语义决定。
func (c *HookContext) Emit(detail string) {
	if c.emit != nil {
		c.emit(SideEffect{Detail: detail})
	}
}

// HookFunc 是钩子的校验逻辑，返回非 nil 错误表示校验失败。
type HookFunc func(ctx *HookContext) error

// Hook 是注册在转移或阶段进入点上的校验钩子。
type Hook struct {
	Name   string
	Kind   HookKind
	From   Stage // 仅 HookTransition：起始阶段
	To     Stage // HookTransition：目标阶段；HookEnter：进入的阶段
	Commit CommitSemantics
	Fn     HookFunc
}

// HookRegistry 按阶段注册并解析钩子触发范围。
// 内部以 (from,to) 与 stage 为键的哈希索引存储，
// 解析开销只与本次转移实际命中的钩子数量相关，
// 与全部转移关系总数、全部已注册钩子总数无关。
type HookRegistry struct {
	byEdge  map[Edge][]*Hook
	byEnter map[Stage][]*Hook
	// scanned 统计最近一次 Resolve 实际扫描（触碰）的钩子数，用于可验证地证明
	// 解析开销与命中数成正比。并发下仅供测试观测，不影响正确性。
	scanned int
}

// NewHookRegistry 创建空注册表。
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		byEdge:  make(map[Edge][]*Hook),
		byEnter: make(map[Stage][]*Hook),
	}
}

// RegisterTransition 在具体转移 (from,to) 上注册钩子。
func (r *HookRegistry) RegisterTransition(h *Hook) {
	h.Kind = HookTransition
	e := Edge{From: h.From, To: h.To}
	r.byEdge[e] = append(r.byEdge[e], h)
}

// RegisterEnter 在阶段进入点上注册钩子。
func (r *HookRegistry) RegisterEnter(h *Hook) {
	h.Kind = HookEnter
	r.byEnter[h.To] = append(r.byEnter[h.To], h)
}

// Resolve 解析一次转移实际命中的钩子：具体转移钩子在前，进入钩子在后。
// 自转移 (from==to) 不做任何特判：仅当声明了 (stage,stage) 的具体转移钩子
// 时才命中具体转移钩子，进入钩子照常命中。
func (r *HookRegistry) Resolve(from, to Stage) []*Hook {
	edgeHooks := r.byEdge[Edge{From: from, To: to}]
	enterHooks := r.byEnter[to]
	r.scanned = len(edgeHooks) + len(enterHooks)
	out := make([]*Hook, 0, r.scanned)
	out = append(out, edgeHooks...)
	out = append(out, enterHooks...)
	return out
}

// LastResolveScanned 返回最近一次 Resolve 扫描的钩子数（= 命中数）。
func (r *HookRegistry) LastResolveScanned() int { return r.scanned }
