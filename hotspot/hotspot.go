// Package hotspot 把按根到叶给出的调用栈样本聚合成调用树，
// 并在节点（按完整路径区分）与函数（按名字去重）两级统计自身值与总值。
//
// 任意已接受样本集合下，本包恒满足：
//   - 全部函数自身值之和 = 已接受样本总权重；
//   - 每个节点的总值 = 节点自身值 + 所有子节点总值之和；
//   - 任一函数的总值 <= 已接受样本总权重（同一样本内递归重复出现只计一次）。
package hotspot

import (
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// RejectReason 描述样本被整体拒绝的具体原因。
type RejectReason int

const (
	// ReasonOK 不是拒绝原因，表示样本被接受。
	ReasonOK RejectReason = iota
	ReasonEmptyStack
	ReasonEmptyFunction
	ReasonInvalidWeight
	ReasonDepthExceeded
	ReasonNodeLimitExceeded
)

// String 返回拒绝原因的稳定可读名称，便于日志与测试断言。
func (r RejectReason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonEmptyStack:
		return "empty_stack"
	case ReasonEmptyFunction:
		return "empty_function_name"
	case ReasonInvalidWeight:
		return "invalid_weight"
	case ReasonDepthExceeded:
		return "depth_exceeded"
	case ReasonNodeLimitExceeded:
		return "node_limit_exceeded"
	default:
		return fmt.Sprintf("unknown_reason_%d", int(r))
	}
}

// Config 为聚合器的上限配置。零值表示对应维度不设限。
type Config struct {
	// MaxDepth 限制单条样本的栈长度（根到叶的层数）。
	MaxDepth int
	// MaxNodes 限制调用树中存活节点（即不同完整路径）的总数。
	MaxNodes int
}

// Option 自定义聚合器行为。
type Option func(*Aggregator)

// WithLogger 设置判定日志的输出位置；传 nil 等价于 io.Discard。
func WithLogger(w io.Writer) Option {
	return func(a *Aggregator) {
		if w == nil {
			w = io.Discard
		}
		a.logger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
	}
}

// StackSample 是一条按根到叶给出的调用栈样本。
type StackSample struct {
	Stack  []string
	Weight int64
}

// NodeStat 是调用树某一节点的统计。节点由从根起的完整路径唯一确定。
type NodeStat struct {
	Path     []string
	Self     int64
	Total    int64
	Children [][]string
}

// FunctionStat 是函数级别的统计；同一样本内重复出现的函数只计一次总值。
type FunctionStat struct {
	Name  string
	Self  int64
	Total int64
}

// Snapshot 是某一时刻满足全部守恒恒等式的一致快照。
type Snapshot struct {
	TotalWeight     int64
	AcceptedSamples int64
	RejectedSamples int64
	NodeCount       int
	FunctionCount   int
	RejectReasons   map[RejectReason]int64
	// Nodes 按完整路径字典序排列。
	Nodes []NodeStat
	// Functions 按函数名字典序排列。
	Functions []FunctionStat
	// HotFunctions 按总值降序、并列按函数名升序排列。
	HotFunctions []FunctionStat
}

type node struct {
	name     string
	path     []string
	parent   *node
	children map[string]*node
	self     int64
	total    int64
}

// Aggregator 聚合调用栈样本。Submit 与 Query 均可被并发调用；
// 每次 Submit 在单一临界区内完成校验、建点与计数，因而可线性化，
// 且对被接受样本集合而言统计满足交换律（与串行提交顺序无关）。
type Aggregator struct {
	cfg    Config
	mu     sync.RWMutex
	logger *slog.Logger

	roots     map[string]*node
	nodes     map[string]*node // 键为路径的稳定序列化形式
	nodeCount int

	funcSelf  map[string]int64
	funcTotal map[string]int64

	totalWeight     int64
	acceptedSamples int64
	rejectedSamples int64
	rejectReasons   map[RejectReason]int64
}

// New 创建带给定上限的聚合器。
func New(cfg Config, opts ...Option) *Aggregator {
	a := &Aggregator{
		cfg:           cfg,
		roots:         make(map[string]*node),
		nodes:         make(map[string]*node),
		funcSelf:      make(map[string]int64),
		funcTotal:     make(map[string]int64),
		rejectReasons: make(map[RejectReason]int64),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func pathKey(path []string) string {
	return strings.Join(path, "\x00")
}

// validate 在不触碰任何状态的前提下完成全部输入校验与容量预检。
func (a *Aggregator) validate(sample StackSample) RejectReason {
	if len(sample.Stack) == 0 {
		return ReasonEmptyStack
	}
	for _, fn := range sample.Stack {
		if fn == "" {
			return ReasonEmptyFunction
		}
	}
	if sample.Weight <= 0 {
		return ReasonInvalidWeight
	}
	if a.cfg.MaxDepth > 0 && len(sample.Stack) > a.cfg.MaxDepth {
		return ReasonDepthExceeded
	}
	if a.cfg.MaxNodes > 0 {
		needed := 0
		path := make([]string, 0, len(sample.Stack))
		for _, fn := range sample.Stack {
			path = append(path, fn)
			if _, ok := a.nodes[pathKey(path)]; !ok {
				needed++
			}
		}
		if a.nodeCount+needed > a.cfg.MaxNodes {
			return ReasonNodeLimitExceeded
		}
	}
	return ReasonOK
}

// Submit 原子地提交一条样本。
// 被拒绝的样本不创建任何节点，也不改变任何统计。
func (a *Aggregator) Submit(sample StackSample) (bool, RejectReason) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if reason := a.validate(sample); reason != ReasonOK {
		a.rejectedSamples++
		a.rejectReasons[reason]++
		a.logger.Info("sample rejected",
			slog.Any("stack", sample.Stack),
			slog.Int64("weight", sample.Weight),
			slog.String("reason", reason.String()),
			slog.String("decision", "reject"),
		)
		return false, reason
	}

	path := make([]string, 0, len(sample.Stack))
	var parent *node
	level := 0
	for _, fn := range sample.Stack {
		path = append(path, fn)
		level++
		branch := a.roots
		if parent != nil {
			branch = parent.children
		}
		cur, ok := branch[fn]
		if !ok {
			cur = &node{
				name:     fn,
				path:     append([]string(nil), path...),
				parent:   parent,
				children: make(map[string]*node),
			}
			branch[fn] = cur
			a.nodes[pathKey(cur.path)] = cur
			a.nodeCount++
		}
		cur.total += sample.Weight
		parent = cur
	}

	leaf := parent
	leaf.self += sample.Weight

	leafFn := sample.Stack[len(sample.Stack)-1]
	a.funcSelf[leafFn] += sample.Weight

	seen := make(map[string]struct{}, len(sample.Stack))
	for _, fn := range sample.Stack {
		if _, dup := seen[fn]; dup {
			continue
		}
		seen[fn] = struct{}{}
		a.funcTotal[fn] += sample.Weight
	}

	a.totalWeight += sample.Weight
	a.acceptedSamples++

	selfNames := selfNames(sample.Stack)
	a.logger.Info("sample accepted",
		slog.Any("stack", sample.Stack),
		slog.Int64("weight", sample.Weight),
		slog.Int("depth", level),
		slog.Any("distinct_functions", selfNames),
		slog.Int("node_count", a.nodeCount),
		slog.Int64("total_weight", a.totalWeight),
		slog.String("decision", "accept"),
		slog.String("basis", "validated; weights added along path; leaf self; distinct functions total"),
	)
	return true, ReasonOK
}

func selfNames(stack []string) []string {
	seen := make(map[string]struct{}, len(stack))
	names := make([]string, 0, len(stack))
	for _, fn := range stack {
		if _, ok := seen[fn]; ok {
			continue
		}
		seen[fn] = struct{}{}
		names = append(names, fn)
	}
	sort.Strings(names)
	return names
}

// Query 返回当前状态的一致快照；快照中的切片均为独立拷贝，调用方可自由修改。
func (a *Aggregator) Query() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	snap := Snapshot{
		TotalWeight:     a.totalWeight,
		AcceptedSamples: a.acceptedSamples,
		RejectedSamples: a.rejectedSamples,
		NodeCount:       a.nodeCount,
		FunctionCount:   len(a.funcTotal),
		RejectReasons:   make(map[RejectReason]int64, len(a.rejectReasons)),
		Nodes:           make([]NodeStat, 0, len(a.nodes)),
		Functions:       make([]FunctionStat, 0, len(a.funcTotal)),
	}
	for reason, count := range a.rejectReasons {
		snap.RejectReasons[reason] = count
	}

	allNodes := make([]*node, 0, len(a.nodes))
	for _, nd := range a.nodes {
		allNodes = append(allNodes, nd)
	}
	sort.Slice(allNodes, func(i, j int) bool {
		return pathKey(allNodes[i].path) < pathKey(allNodes[j].path)
	})
	for _, nd := range allNodes {
		childNames := make([]string, 0, len(nd.children))
		for name := range nd.children {
			childNames = append(childNames, name)
		}
		sort.Strings(childNames)
		children := make([][]string, 0, len(childNames))
		for _, name := range childNames {
			children = append(children, append(append([]string(nil), nd.path...), name))
		}
		snap.Nodes = append(snap.Nodes, NodeStat{
			Path:     append([]string(nil), nd.path...),
			Self:     nd.self,
			Total:    nd.total,
			Children: children,
		})
	}

	for name, total := range a.funcTotal {
		snap.Functions = append(snap.Functions, FunctionStat{
			Name:  name,
			Self:  a.funcSelf[name],
			Total: total,
		})
	}
	sort.Slice(snap.Functions, func(i, j int) bool {
		return snap.Functions[i].Name < snap.Functions[j].Name
	})

	snap.HotFunctions = append([]FunctionStat(nil), snap.Functions...)
	sort.SliceStable(snap.HotFunctions, func(i, j int) bool {
		if snap.HotFunctions[i].Total != snap.HotFunctions[j].Total {
			return snap.HotFunctions[i].Total > snap.HotFunctions[j].Total
		}
		return snap.HotFunctions[i].Name < snap.HotFunctions[j].Name
	})

	a.logger.Info("query snapshot",
		slog.Int64("total_weight", snap.TotalWeight),
		slog.Int64("accepted", snap.AcceptedSamples),
		slog.Int64("rejected", snap.RejectedSamples),
		slog.Int("nodes", snap.NodeCount),
		slog.Int("functions", snap.FunctionCount),
		slog.Any("hot_functions", snap.HotFunctions),
		slog.String("basis", "invariant snapshot; hot order total desc then name asc"),
	)
	return snap
}
