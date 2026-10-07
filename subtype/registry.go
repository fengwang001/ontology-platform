package subtype

import "sync"

// Registry 保存已登记的命名类型定义，并提供子类型判定。
// 所有方法可并发调用，效果等价于按某个串行顺序执行。
type Registry struct {
	mu   sync.RWMutex
	defs map[string]Type
}

// NewRegistry 创建一个空的注册表。
func NewRegistry() *Registry {
	return &Registry{defs: make(map[string]Type)}
}

// Register 登记一个命名类型定义。定义可以引用尚未登记的名字（前向引用），
// 未定义引用会在判定时报错。参数非法优先于重复定义；
// 被拒绝的登记不改变任何状态。
func (r *Registry) Register(name string, def Type) error {
	if name == "" {
		return &Error{Kind: KindInvalidArgument, Detail: "empty type name"}
	}
	if err := validateExpr(def); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs[name]; ok {
		return &Error{Kind: KindDuplicateDefinition, Name: name}
	}
	r.defs[name] = def
	return nil
}

// Check 判定 left 是否为 right 的子类型。
func (r *Registry) Check(left, right Type) (bool, error) {
	ok, _, err := r.CheckWithStats(left, right)
	return ok, err
}

// CheckWithStats 与 Check 相同，但额外返回本次判定的统计信息，
// 用于验证命名对检查数量的上界。
//
// 错误优先级：参数非法 > 未定义引用 > 无保护循环。
// 未定义引用与无保护循环对两个待比较类型静态可达的全部命名定义检查，
// 各自报告字典序最小的命名类型名。
func (r *Registry) CheckWithStats(left, right Type) (bool, Stats, error) {
	if err := validateExpr(left); err != nil {
		return false, Stats{}, err
	}
	if err := validateExpr(right); err != nil {
		return false, Stats{}, err
	}
	// 在读锁下拷贝定义快照，判定在快照上进行，不阻塞并发登记。
	// 快照拷贝的时刻即为本次判定的线性化点。
	r.mu.RLock()
	snapshot := make(map[string]Type, len(r.defs))
	for name, def := range r.defs {
		snapshot[name] = def
	}
	r.mu.RUnlock()

	reachLeft := reachableNames(snapshot, left)
	reachRight := reachableNames(snapshot, right)
	merged := make(map[string]bool, len(reachLeft)+len(reachRight))
	for name := range reachLeft {
		merged[name] = true
	}
	for name := range reachRight {
		merged[name] = true
	}
	stats := Stats{
		ReachableLeft:  len(reachLeft),
		ReachableRight: len(reachRight),
		ReachableUnion: len(merged),
	}
	if name, ok := findUndefined(snapshot, merged); ok {
		return false, stats, &Error{Kind: KindUndefinedReference, Name: name}
	}
	if name, ok := findUnguardedCycle(snapshot, merged); ok {
		return false, stats, &Error{Kind: KindUnguardedCycle, Name: name}
	}
	return subtypeOf(snapshot, left, right, &stats), stats, nil
}
