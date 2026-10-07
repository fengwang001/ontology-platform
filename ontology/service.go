package ontology

import "sort"

// HiddenKind 描述可见前缀边界之后不可见延伸的聚合判定。
type HiddenKind int

const (
	// HiddenNone 不存在不可见延伸。
	HiddenNone HiddenKind = iota
	// HiddenCycle 不可见延伸中存在一条在完整图上、剩余深度内闭合的真实环路。
	HiddenCycle
	// HiddenPartial 存在不可见延伸但剩余深度内不闭合，成环与否对调用方未知。
	HiddenPartial
)

// Hop 是返回路径中的一跳，只包含调用方可见的链接信息。
type Hop struct {
	LinkID string
	From   string
	To     string
	Label  string
}

// Verdict 是一条返回路径在完整图层面的判定。
type Verdict int

const (
	// VerdictNonCyclic 路径完全可见且在完整图上不构成环路，正常返回。
	VerdictNonCyclic Verdict = iota
	// VerdictVisibleCycle 路径完全可见，末跳在完整图上闭合真实环路。
	VerdictVisibleCycle
	// VerdictHiddenCycle 可见前缀之后的不可见延伸闭合真实环路，止于边界。
	VerdictHiddenCycle
	// VerdictPartialInvisible 可见前缀后存在不可见延伸，不成环，成环未知。
	VerdictPartialInvisible
)

// PathResult 是返回给调用方的一条路径。
// 任何不可见链接的具体内容（ID、标签、连接对象标识）均不出现在这里。
type PathResult struct {
	Prefix  []Hop
	End     string
	Verdict Verdict
	Hidden  HiddenKind
}

// TraverseRequest 是一次遍历请求。
type TraverseRequest struct {
	CallerID string
	Start    string
	Labels   map[string]struct{}
	MaxDepth int
}

// TraverseResponse 是一次遍历的结果。
type TraverseResponse struct {
	SnapshotVersion int64
	Paths           []PathResult
	// AncestorChecks 本次遍历完整图环路判定的祖先序列核对总次数。
	AncestorChecks int
}

// Logger 记录每次遍历的调用方权限、输入与各路径判定依据。
type Logger interface {
	Log(entry LogEntry)
}

// LogEntry 为单次遍历的日志记录，只包含允许返回给调用方的信息。
type LogEntry struct {
	CallerID        string
	Start           string
	Labels          []string
	MaxDepth        int
	SnapshotVersion int64
	Paths           []PathResult
	AncestorChecks  int
}

// Service 是受权限约束的图遍历环检测服务。
type Service struct {
	store  *GraphStore
	logger Logger
}

// NewService 创建遍历服务。
func NewService(store *GraphStore, logger Logger) *Service {
	return &Service{store: store, logger: logger}
}

// Traverse 执行一次受权限约束的遍历环检测。
func (s *Service) Traverse(req TraverseRequest) (*TraverseResponse, error) {
	snap := s.store.Snapshot()
	return s.traverseSnapshot(snap, req)
}

// traverseSnapshot 在给定快照上执行遍历，供测试注入确定快照复用。
func (s *Service) traverseSnapshot(snap *Snapshot, req TraverseRequest) (*TraverseResponse, error) {
	// 固定次序的四类错误校验，只报第一类。
	if _, ok := snap.objects[req.Start]; !ok {
		return nil, ErrStartNotFound
	}
	if len(req.Labels) == 0 {
		return nil, ErrEmptyLabels
	}
	if req.MaxDepth <= 0 {
		return nil, ErrInvalidDepth
	}
	if !objectVisible(snap, req.Start, req.Labels) {
		return nil, ErrStartNotVisible
	}

	t := &traversal{snap: snap, labels: req.Labels, maxDepth: req.MaxDepth}
	paths := t.walkVisible(req.Start, nil, map[string]int{req.Start: 0})

	// 相同完整路径（可见前缀逐跳一致 + 判定一致）在不同游走间去重，
	// 去重不改变判定集合，仅消除枚举重复。
	paths = dedupPaths(paths)

	resp := &TraverseResponse{
		SnapshotVersion: snap.version,
		Paths:           paths,
		AncestorChecks:  t.checks,
	}
	if s.logger != nil {
		s.logger.Log(LogEntry{
			CallerID:        req.CallerID,
			Start:           req.Start,
			Labels:          sortedLabels(req.Labels),
			MaxDepth:        req.MaxDepth,
			SnapshotVersion: snap.version,
			Paths:           paths,
			AncestorChecks:  t.checks,
		})
	}
	return resp, nil
}

type traversal struct {
	snap     *Snapshot
	labels   map[string]struct{}
	maxDepth int
	checks   int // 完整图环路判定的祖先序列核对次数
}

// objectVisible 判定对象对调用方是否可见：
// 存在至少一条以其为端点且标签被授予的链接即可见。
func objectVisible(snap *Snapshot, obj string, labels map[string]struct{}) bool {
	for _, lk := range snap.incident[obj] {
		if _, ok := labels[lk.Label]; ok {
			return true
		}
	}
	return false
}

// walkVisible 沿“仅可见链接”做深度受限的游走，返回从当前可见前缀
// 延伸出去的全部路径结果。ancestors 是当前游走在完整图上的祖先序列
// （可见游走阶段即路径本身），值为该对象在序列中的位置。
func (t *traversal) walkVisible(node string, prefix []Hop, ancestors map[string]int) []PathResult {
	depth := len(prefix)
	var results []PathResult

	for _, lk := range t.snap.out[node] {
		if _, ok := t.labels[lk.Label]; !ok {
			continue // 该链接对调用方不可见，不单独枚举
		}
		// 完整图层环判定：一次 O(1) 哈希核对，与图/对象/标签总量无关。
		t.checks++
		if _, cyclic := ancestors[lk.To]; cyclic {
			results = append(results, PathResult{
				Prefix:  appendHop(prefix, lk),
				End:     lk.To,
				Verdict: VerdictVisibleCycle,
				Hidden:  HiddenNone,
			})
			continue // 真实环路：全调用方一致地终止该方向
		}
		// 超过深度上限的跳不再展开；恰好到达上限的最后一跳仍递归，
		// 由目标节点统一产出非环记录（保持路径前缀完整）。
		if depth+1 > t.maxDepth {
			continue
		}
		nextAncestors := cloneAncestors(ancestors)
		nextAncestors[lk.To] = depth + 1
		results = append(results, t.walkVisible(lk.To, appendHop(prefix, lk), nextAncestors)...)
	}

	// 前沿节点的统一产出（与朴素参考实现对齐）：
	//  - 深度上限到达：该可见路径按非环正常返回（逐边记录已在上面产生）。
	//  - 存在不可见出边：同一前沿至多一个聚合标记（CYCE/PARTIAL）。
	//  - 否则若为完整图死路：补一条非环路径记录。
	// 可见出边在未到深度上限时必然已在上面递归展开，因此这里只处理
	// “无可见出边可继续”的节点。
	// 到达深度上限的可见路径：按非环正常返回。
	if depth > 0 && depth >= t.maxDepth {
		hasHidden := false
		for _, lk := range t.snap.out[node] {
			if _, ok := t.labels[lk.Label]; !ok {
				hasHidden = true
				break
			}
		}
		if hasHidden {
			results = append(results, PathResult{
				Prefix:  cloneHops(prefix),
				End:     node,
				Verdict: VerdictPartialInvisible,
				Hidden:  HiddenPartial,
			})
			return results
		}
		results = append(results, PathResult{
			Prefix:  cloneHops(prefix),
			End:     node,
			Verdict: VerdictNonCyclic,
			Hidden:  HiddenNone,
		})
		return results
	}
	hidden := t.classifyHidden(node, ancestors, t.maxDepth-depth)
	if hidden != HiddenNone {
		verdict := VerdictPartialInvisible
		if hidden == HiddenCycle {
			verdict = VerdictHiddenCycle
		}
		results = append(results, PathResult{
			Prefix:  cloneHops(prefix),
			End:     node,
			Verdict: verdict,
			Hidden:  hidden,
		})
	}
	// 完整图死路（无任何出链接）的可见路径，补一条非环记录。
	if depth > 0 && len(t.snap.out[node]) == 0 {
		results = append(results, PathResult{
			Prefix:  cloneHops(prefix),
			End:     node,
			Verdict: VerdictNonCyclic,
			Hidden:  HiddenNone,
		})
	}

	return results
}

// classifyHidden 判定从可见节点 node 经“首跳即不可见”的链接进入隐藏区后，
// 在剩余深度 remaining 内是否存在闭合真实环路。
// 返回 HiddenCycle / HiddenPartial / HiddenNone。
// ancestors 为当前可见游走的祖先序列；进入隐藏区后的访问集合在
// hiddenDFS 内部维护。成本只取决于剩余深度与隐藏区分支，与全图规模无关。
func (t *traversal) classifyHidden(node string, ancestors map[string]int, remaining int) HiddenKind {
	exists := false
	for _, lk := range t.snap.out[node] {
		if _, ok := t.labels[lk.Label]; ok {
			continue
		}
		exists = true
		visited := cloneAncestors(ancestors)
		if _, cyclic := visited[lk.To]; cyclic {
			return HiddenCycle // 首条不可见链接直接闭合到可见祖先
		}
		// 首跳已确定为不可见链接；进入隐藏区后深度加一。
		visited[lk.To] = len(visited)
		if t.hiddenCycle(lk.To, visited, len(ancestors)+1, remaining-1) {
			return HiddenCycle
		}
	}
	if exists {
		return HiddenPartial
	}
	return HiddenNone

}

// hiddenCycle 在“对调用方完全不可见”的区域内做深度受限的完整图环判定。
// 一旦遇到当前游走祖先序列中的对象即判定真实环路（终止）。
// visited 记录本次游走已访问对象（含可见祖先与隐藏区祖先），值为深度；
// 非祖先重复访问同样被剪枝（同一节点的后续可达性与到达路径无关）。
// depth 为 node 在游走中的深度（跳数），remaining 为还可继续的跳数。
func (t *traversal) hiddenCycle(node string, visited map[string]int, depth, remaining int) bool {
	for _, lk := range t.snap.out[node] {
		t.checks++
		if _, seen := visited[lk.To]; seen {
			return true // 末跳闭合同样是真实环路，即使剩余深度为 0
		}
		if remaining == 0 {
			continue
		}
		nextVisited := cloneAncestors(visited)
		nextVisited[lk.To] = depth + 1
		if t.hiddenCycle(lk.To, nextVisited, depth+1, remaining-1) {
			return true
		}
	}
	return false
}

func appendHop(prefix []Hop, lk *Link) []Hop {
	out := make([]Hop, 0, len(prefix)+1)
	out = append(out, prefix...)
	out = append(out, Hop{LinkID: lk.ID, From: lk.From, To: lk.To, Label: lk.Label})
	return out
}

func cloneHops(hops []Hop) []Hop {
	if len(hops) == 0 {
		return nil
	}
	out := make([]Hop, len(hops))
	copy(out, hops)
	return out
}

func cloneAncestors(in map[string]int) map[string]int {
	out := make(map[string]int, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedLabels(labels map[string]struct{}) []string {
	out := make([]string, 0, len(labels))
	for l := range labels {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func dedupPaths(paths []PathResult) []PathResult {
	seen := map[string]struct{}{}
	out := make([]PathResult, 0, len(paths))
	for _, p := range paths {
		key := pathKey(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}

func pathKey(p PathResult) string {
	key := make([]byte, 0, 64)
	key = append(key, p.End...)
	key = append(key, ';')
	for _, h := range p.Prefix {
		key = append(key, h.LinkID...)
		key = append(key, '>')
	}
	key = append(key, '|')
	key = append(key, byte('0'+p.Verdict))
	key = append(key, byte('0'+p.Hidden))
	return string(key)
}
