package ontology

// Direction 指定一跳扩展的方向。
type Direction int

const (
	DirOut Direction = iota
	DirIn
	DirBoth
)

// HopSpec 描述一跳的扩展规则。
type HopSpec struct {
	LinkType string    // 空串表示不限链接类型
	Dir      Direction // 扩展方向
	Limit    int       // 每个节点在这一跳上最多取用的候选数；<=0 表示不限
}

// TraversalSpec 描述一次完整遍历。
type TraversalSpec struct {
	Start ObjectID
	Hops  []HopSpec
}

// Mode 是限额处理模式，首次请求确定后整次遍历不可更改。
type Mode int

const (
	ModeSilent Mode = iota // 静默丢弃：超限候选直接丢弃，不留痕迹
	ModeMarked             // 显式截断标记：超限处附带截断标记
)

// TruncationMarker 指出某一跳上发生了候选丢弃（仅 ModeMarked ）。
type TruncationMarker struct {
	Hop     int      // 1 起始的跳序号
	At      ObjectID // 在哪个节点的扩展上发生截断
	Dropped int      // 被丢弃的候选数量
}

// iterator 在固定快照上增量执行遍历，按确定次序逐条产出结果。
// 起始对象本身不计入结果。
type iterator struct {
	store *Store
	snap  uint64
	spec  TraversalSpec
	mode  Mode

	// 已发现但尚未产出的结果队列（BFS 发现序即产出序）。
	emitQ    []ObjectID
	emitHead int

	// 待扩展前沿。
	frontier  []frontierNode
	frontHead int

	// 当前正在扩展的节点的候选列表与消费位置。
	cands   []candidate
	candIdx int

	visited map[ObjectID]struct{}

	// 本页内新产生的截断标记，由服务层取走。
	pendingMarkers []TruncationMarker

	// 指标：为判定“是否已返回过”而触碰的历史记录（visited 查询）次数。
	historyProbes int
}

// frontierNode 是待扩展前沿中的一个节点。
type frontierNode struct {
	obj   ObjectID
	depth int
}

func newIterator(store *Store, snap uint64, spec TraversalSpec, mode Mode) *iterator {
	it := &iterator{
		store:   store,
		snap:    snap,
		spec:    spec,
		mode:    mode,
		visited: make(map[ObjectID]struct{}),
	}
	it.visited[spec.Start] = struct{}{}
	it.frontier = []frontierNode{{obj: spec.Start, depth: 0}}
	return it
}

// next 产出下一条结果；ok=false 表示遍历在快照上已耗尽。
func (it *iterator) next() (id ObjectID, ok bool) {
	for {
		if it.emitHead < len(it.emitQ) {
			id = it.emitQ[it.emitHead]
			it.emitHead++
			if it.emitHead == len(it.emitQ) {
				it.emitQ = it.emitQ[:0]
				it.emitHead = 0
			}
			return id, true
		}
		if !it.expandStep() {
			return "", false
		}
	}
}

// expandStep 推进前沿扩展，直到至少产出一条新结果或前沿耗尽。
// 返回 false 表示快照上已无任何可扩展内容。
func (it *iterator) expandStep() bool {
	for it.frontHead < len(it.frontier) {
		node := it.frontier[it.frontHead]
		if node.depth >= len(it.spec.Hops) {
			it.frontHead++
			continue
		}
		if it.cands == nil {
			hop := it.spec.Hops[node.depth]
			all := it.store.neighborsAt(it.snap, node.obj, hop.LinkType, hop.Dir)
			take := len(all)
			if hop.Limit > 0 && hop.Limit < take {
				take = hop.Limit
			}
			if dropped := len(all) - take; dropped > 0 && it.mode == ModeMarked {
				it.pendingMarkers = append(it.pendingMarkers, TruncationMarker{
					Hop:     node.depth + 1,
					At:      node.obj,
					Dropped: dropped,
				})
			}
			it.cands = all[:take]
			it.candIdx = 0
		}
		produced := false
		for it.candIdx < len(it.cands) {
			c := it.cands[it.candIdx]
			it.candIdx++
			it.historyProbes++
			if _, seen := it.visited[c.neighbor]; seen {
				continue
			}
			it.visited[c.neighbor] = struct{}{}
			it.emitQ = append(it.emitQ, c.neighbor)
			it.frontier = append(it.frontier, frontierNode{obj: c.neighbor, depth: node.depth + 1})
			produced = true
		}
		it.cands = nil
		it.frontHead++
		if produced {
			return true
		}
	}
	return false
}

// takeMarkers 取走并清空迭代器累计的截断标记。
func (it *iterator) takeMarkers() []TruncationMarker {
	m := it.pendingMarkers
	it.pendingMarkers = nil
	return m
}

// hasMore 报告快照上是否还有未产出的结果。可能触发惰性的前沿扩展，
// 该扩展是确定性的，提前执行不改变产出次序。
func (it *iterator) hasMore() bool {
	if it.emitHead < len(it.emitQ) {
		return true
	}
	return it.expandStep()
}
