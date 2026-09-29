package hotspot

import (
	"sort"
	"sync"
)

// RejectReason 标识样本被整体拒绝的原因。
type RejectReason string

const (
	ReasonEmptyStack    RejectReason = "empty_stack"
	ReasonEmptyName     RejectReason = "empty_function_name"
	ReasonInvalidWeight RejectReason = "invalid_weight"
	ReasonDepthLimit    RejectReason = "depth_exceeded"
	ReasonNodeLimit     RejectReason = "node_limit_exceeded"
)

// Sample 是一条按根到叶给出的调用栈样本。
type Sample struct {
	Stack  []string
	Weight int64
}

// Config 控制聚合器的上限。非正值使用默认值。
type Config struct {
	MaxDepth int
	MaxNodes int
}

const (
	defaultMaxDepth = 1000
	defaultMaxNodes = 100000
)

// NodeStat 是调用树单个节点的一致快照；节点按从根起的完整路径区分。
type NodeStat struct {
	Path     []string
	Function string
	Self     int64
	Total    int64
}

// FunctionStat 是函数级统计的一致快照。
type FunctionStat struct {
	Function string
	Self     int64
	Total    int64
}

// Snapshot 是一次查询返回的不可变一致快照。
type Snapshot struct {
	Nodes       []NodeStat
	Functions   []FunctionStat
	TotalWeight int64
	Rejected    map[RejectReason]int64
}

// Logger 用于打印每次提交/查询的输入、输出与判定依据，可为 nil。
type Logger interface {
	Printf(format string, args ...any)
}

type node struct {
	function string
	children map[string]*node
	self     int64
	total    int64
}

type funcStat struct {
	self  int64
	total int64
}

// Aggregator 并发安全地聚合调用栈样本。
type Aggregator struct {
	mu        sync.Mutex
	maxDepth  int
	maxNodes  int
	roots     map[string]*node
	nodeCount int
	functions map[string]*funcStat
	total     int64
	rejected  map[RejectReason]int64
	logger    Logger
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = defaultMaxDepth
	}
	if cfg.MaxNodes <= 0 {
		cfg.MaxNodes = defaultMaxNodes
	}
	return cfg
}

// New 创建带默认上限的聚合器。
func New() *Aggregator {
	return NewWithConfig(Config{})
}

// NewWithConfig 创建自定义上限的聚合器。
func NewWithConfig(cfg Config) *Aggregator {
	cfg = normalizeConfig(cfg)
	return &Aggregator{
		maxDepth:  cfg.MaxDepth,
		maxNodes:  cfg.MaxNodes,
		roots:     map[string]*node{},
		functions: map[string]*funcStat{},
		rejected:  map[RejectReason]int64{},
	}
}

// WithLogger 关联日志输出，便于链式构造。
func (a *Aggregator) WithLogger(logger Logger) *Aggregator {
	a.mu.Lock()
	a.logger = logger
	a.mu.Unlock()
	return a
}

// validate 按固定顺序检查样本合法性，返回拒绝原因；空串表示合法。
func validate(s Sample, maxDepth int) RejectReason {
	if len(s.Stack) == 0 {
		return ReasonEmptyStack
	}
	for _, fn := range s.Stack {
		if fn == "" {
			return ReasonEmptyName
		}
	}
	if s.Weight <= 0 {
		return ReasonInvalidWeight
	}
	if len(s.Stack) > maxDepth {
		return ReasonDepthLimit
	}
	return ""
}

// Submit 原子地提交一条样本；被拒绝时样本不改变任何统计。
// 采用“先离线构造路径、再整体挂树”的两阶段做法：节点上限触发
// 拒绝时，新建节点只游离在局部切片中，树与统计均不发生变化。
func (a *Aggregator) Submit(s Sample) (accepted bool, reason RejectReason) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if reason = validate(s, a.maxDepth); reason != "" {
		a.rejected[reason]++
		a.log("reject input=%v weight=%d output=accepted:false reason=%s basis=%s",
			s.Stack, s.Weight, reason, "validation_failed")
		return false, reason
	}

	// 第一阶段：沿根到叶路径离线遍历，缺失的节点先新建但不挂树。
	pathNodes := make([]*node, 0, len(s.Stack))
	children := a.roots
	newNeeded := 0
	for _, fn := range s.Stack {
		if n, ok := children[fn]; ok {
			pathNodes = append(pathNodes, n)
			children = n.children
			continue
		}
		if a.nodeCount+newNeeded+1 > a.maxNodes {
			a.rejected[ReasonNodeLimit]++
			a.log("reject input=%v weight=%d output=accepted:false reason=%s basis=node_count=%d,max_nodes=%d,new_needed=%d",
				s.Stack, s.Weight, ReasonNodeLimit, a.nodeCount, a.maxNodes, newNeeded+1)
			return false, ReasonNodeLimit
		}
		n := &node{function: fn, children: map[string]*node{}}
		pathNodes = append(pathNodes, n)
		children = n.children
		newNeeded++
	}

	// 第二阶段：把缺失节点挂入树中（前置校验已通过，不会再失败）。
	children = a.roots
	for depth, fn := range s.Stack {
		if n, ok := children[fn]; ok {
			children = n.children
			continue
		}
		n := pathNodes[depth]
		children[fn] = n
		a.nodeCount++
		children = n.children
	}

	weight := s.Weight
	leaf := pathNodes[len(pathNodes)-1]
	leaf.self += weight
	for _, n := range pathNodes {
		n.total += weight
	}

	// 函数级统计：同一样本中同一函数出现多次只计一次总值；
	// 自身值只累加给叶位置的函数。
	seen := make(map[string]struct{}, len(s.Stack))
	leafFn := s.Stack[len(s.Stack)-1]
	a.getOrCreateFunc(leafFn).self += weight
	for _, fn := range s.Stack {
		if _, dup := seen[fn]; dup {
			continue
		}
		seen[fn] = struct{}{}
		a.getOrCreateFunc(fn).total += weight
	}

	a.total += weight
	a.log("accept input=%v weight=%d output=accepted:true basis=%s nodes=%d total_weight=%d",
		s.Stack, weight, "path_traversed", a.nodeCount, a.total)
	return true, ""
}

func (a *Aggregator) getOrCreateFunc(name string) *funcStat {
	fs := a.functions[name]
	if fs == nil {
		fs = &funcStat{}
		a.functions[name] = fs
	}
	return fs
}

// Query 返回满足全部守恒恒等式的一致快照。
func (a *Aggregator) Query() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	snap := Snapshot{
		TotalWeight: a.total,
		Rejected:    make(map[RejectReason]int64, len(a.rejected)),
	}
	for reason, count := range a.rejected {
		snap.Rejected[reason] = count
	}

	snap.Nodes = a.collectNodes()

	names := make([]string, 0, len(a.functions))
	for name := range a.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fs := a.functions[name]
		snap.Functions = append(snap.Functions, FunctionStat{
			Function: name,
			Self:     fs.self,
			Total:    fs.total,
		})
	}

	// 热点列表：总值降序，并列按函数名升序。
	sort.SliceStable(snap.Functions, func(i, j int) bool {
		if snap.Functions[i].Total != snap.Functions[j].Total {
			return snap.Functions[i].Total > snap.Functions[j].Total
		}
		return snap.Functions[i].Function < snap.Functions[j].Function
	})

	a.log("query output=nodes:%d,functions:%d,total_weight:%d basis=%s",
		len(snap.Nodes), len(snap.Functions), snap.TotalWeight, "consistent_snapshot")
	return snap
}

// collectNodes 在锁内按确定顺序（前序、兄弟按函数名升序）导出全部树节点。
func (a *Aggregator) collectNodes() []NodeStat {
	var out []NodeStat
	var walk func(children map[string]*node, prefix []string)
	walk = func(children map[string]*node, prefix []string) {
		names := make([]string, 0, len(children))
		for name := range children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			n := children[name]
			path := make([]string, 0, len(prefix)+1)
			path = append(path, prefix...)
			path = append(path, name)
			out = append(out, NodeStat{
				Path:     path,
				Function: name,
				Self:     n.self,
				Total:    n.total,
			})
			walk(n.children, path)
		}
	}
	walk(a.roots, nil)
	return out
}

func (a *Aggregator) log(format string, args ...any) {
	if a.logger != nil {
		a.logger.Printf(format, args...)
	}
}
