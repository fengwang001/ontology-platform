package ontology

import (
	"slices"
	"sort"
)

// Termination 描述一条路径分支在遍历中的终止/扩展方式。
type Termination int

const (
	// TermExtended 表示该分支是一条对调用方可见的链接，已正常扩展。
	TermExtended Termination = iota
	// TermCycle 表示该方向在完整图上被判定为真实环路并终止扩展。
	// 无论闭合链接对调用方是否可见，判定结论一致。
	TermCycle
	// TermHidden 表示该方向存在对调用方不可见的延伸，
	// 该延伸本身不构成环路，是否成环未知。
	TermHidden
)

// String 返回终止方式的可读描述。
func (t Termination) String() string {
	switch t {
	case TermExtended:
		return "extended"
	case TermCycle:
		return "cycle"
	case TermHidden:
		return "hidden"
	default:
		return "unknown"
	}
}

// Edge 是遍历结果树中从某个对象出发的一条分支。
//
// 对于对调用方不可见的链接，只允许泄露「存在性」本身：
// Kind 为 TermCycle 或 TermHidden 且 Visible 为 false 时，
// LinkID、LinkLabel、To 均为零值，不得携带任何被隐藏链接的属性。
type Edge struct {
	Kind      Termination
	Visible   bool
	LinkID    string
	LinkLabel Label
	To        ObjectID
	Child     *Node // 仅 Kind 为 TermExtended 时非空
}

// Node 是遍历结果树中的一个对象节点。
type Node struct {
	Object ObjectID
	Edges  []*Edge
}

// Request 是一次遍历的输入。
type Request struct {
	CallerLabels []Label // 调用方被授予的权限标签集合
	Start        ObjectID
	MaxDepth     int
}

// Stats 记录单次遍历的可观测核对指标，用于验证祖先序列核对次数的上界。
type Stats struct {
	// AncestorChecks 是完整图环路判定所触及的祖先序列核对次数。
	// 基于哈希集合的实现保证每扩展一条链接恰好核对 1 次，
	// 与图中对象、链接与权限标签种类总数无关。
	AncestorChecks int64
	// ExpandedLinks 是遍历过程中实际考察过的链接总数。
	ExpandedLinks int64
}

// Result 是一次遍历的完整输出。
type Result struct {
	Root    *Node
	Stats   Stats
	Version uint64 // 本次遍历所基于的快照版本
}

// Traverse 在 store 的某一确定快照上执行受权限约束的遍历与完整图环检测。
// 等价于 store.Snapshot() 后立即调用 Snapshot.Traverse。
func Traverse(store *Store, req Request, logger Logger) (*Result, error) {
	return store.Snapshot().Traverse(req, logger)
}

// Traverse 在快照上执行遍历。快照在遍历期间不可变，
// 因此环路判定与可见性判定共享同一个时点。
//
// 错误判定次序固定（互斥，只报第一类）：
// 起始对象不存在 -> 调用方标签集合为空 -> 深度上限非正整数 -> 起始对象不可见。
func (snap *Snapshot) Traverse(req Request, logger Logger) (*Result, error) {
	if _, ok := snap.objects[req.Start]; !ok {
		return nil, ErrStartNotFound
	}
	if len(req.CallerLabels) == 0 {
		return nil, ErrEmptyCallerLabels
	}
	if req.MaxDepth <= 0 {
		return nil, ErrInvalidMaxDepth
	}
	caller := make(map[Label]struct{}, len(req.CallerLabels))
	for _, l := range req.CallerLabels {
		caller[l] = struct{}{}
	}
	if !snap.visibleTo(req.Start, caller) {
		return nil, ErrStartInvisible
	}

	t := &traverser{
		snap:         snap,
		caller:       caller,
		callerSorted: slices.Clone(req.CallerLabels),
		maxDepth:     req.MaxDepth,
		start:        req.Start,
		onPath:       make(map[ObjectID]struct{}),
		logger:       logger,
	}
	sort.Slice(t.callerSorted, func(i, j int) bool { return t.callerSorted[i] < t.callerSorted[j] })
	t.log(DecisionBegin, "", "遍历开始")
	root := t.expand(req.Start, 0)
	t.log(DecisionEnd, "", "遍历结束")
	return &Result{Root: root, Stats: t.stats, Version: snap.version}, nil
}

// visibleTo 判定对象对调用方是否可见：
// 对象至少关联一条（入或出）权限标签在调用方集合内的链接时可见。
func (snap *Snapshot) visibleTo(obj ObjectID, caller map[Label]struct{}) bool {
	for _, l := range snap.out[obj] {
		if _, ok := caller[l.Label]; ok {
			return true
		}
	}
	for _, l := range snap.in[obj] {
		if _, ok := caller[l.Label]; ok {
			return true
		}
	}
	return false
}

// traverser 是单次遍历的内部状态。
type traverser struct {
	snap         *Snapshot
	caller       map[Label]struct{}
	callerSorted []Label
	maxDepth     int
	start        ObjectID
	stats        Stats
	logger       Logger

	onPath map[ObjectID]struct{} // 当前路径上的对象集合，O(1) 祖先核对
	path   []string              // 当前路径上的可见链接 ID，用于日志
}

// expand 从 obj 出发扩展一层。depth 为当前路径上的链接数。
func (t *traverser) expand(obj ObjectID, depth int) *Node {
	node := &Node{Object: obj}
	if depth >= t.maxDepth {
		return node
	}
	t.onPath[obj] = struct{}{}
	defer delete(t.onPath, obj)

	hiddenCycle := false // 存在不可见链接闭合的真实环路
	hiddenExt := false   // 存在不可见且不成环的延伸

	for _, l := range t.snap.out[obj] {
		t.stats.ExpandedLinks++
		t.stats.AncestorChecks++ // 每条链接恰好一次 O(1) 祖先核对
		_, cycle := t.onPath[l.To]
		_, visible := t.caller[l.Label]
		switch {
		case cycle && visible:
			// 可见链接闭合的环路：目标已在调用方可见路径上，可以指明。
			node.Edges = append(node.Edges, &Edge{
				Kind: TermCycle, Visible: true,
				LinkID: l.ID, LinkLabel: l.Label, To: l.To,
			})
			t.logAt(obj, DecisionCycle, l.ID, "可见链接在完整图上闭合为真实环路，终止扩展")
		case cycle:
			// 仅借助不可见链接才能闭合的环路：只告知成环事实。
			hiddenCycle = true
			t.logAt(obj, DecisionCycle, "", "不可见方向在完整图上闭合为真实环路，终止扩展（链接细节已隐藏）")
		case !visible:
			// 不可见且不成环的延伸：标记「部分不可见」，是否成环未知。
			hiddenExt = true
			t.logAt(obj, DecisionHidden, "", "存在不可见延伸，该延伸本身不成环，是否成环未知")
		default:
			t.path = append(t.path, l.ID)
			child := t.expand(l.To, depth+1)
			t.path = t.path[:len(t.path)-1]
			node.Edges = append(node.Edges, &Edge{
				Kind: TermExtended, Visible: true,
				LinkID: l.ID, LinkLabel: l.Label, To: l.To,
				Child: child,
			})
			t.logAt(obj, DecisionExtended, l.ID, "可见链接且不构成环路，正常扩展")
		}
	}
	// 不可见标记每节点至多各一条：只泄露存在性，不泄露数量与属性。
	if hiddenCycle {
		node.Edges = append(node.Edges, &Edge{Kind: TermCycle})
	}
	if hiddenExt {
		node.Edges = append(node.Edges, &Edge{Kind: TermHidden})
	}
	return node
}

func (t *traverser) log(kind DecisionKind, linkID, reason string) {
	t.logAt(t.start, kind, linkID, reason)
}

func (t *traverser) logAt(at ObjectID, kind DecisionKind, linkID, reason string) {
	if t.logger == nil {
		return
	}
	t.logger.Log(LogEntry{
		Kind:         kind,
		CallerLabels: slices.Clone(t.callerSorted),
		Start:        t.start,
		MaxDepth:     t.maxDepth,
		Version:      t.snap.version,
		Path:         slices.Clone(t.path),
		At:           at,
		LinkID:       linkID,
		Reason:       reason,
	})
}
