package ontology

import (
	"container/heap"
	"fmt"
)

// QueryStatus 是路径查询的最终状态。
type QueryStatus int8

const (
	// StatusReachable 存在允许遍历的最短路径。
	StatusReachable QueryStatus = iota + 1
	// StatusUnreachable 不存在任何允许遍历的完整路径。
	StatusUnreachable
	// StatusAmbiguous 因覆盖判定歧义无法给出唯一结果。
	StatusAmbiguous
)

func (s QueryStatus) String() string {
	switch s {
	case StatusReachable:
		return "reachable"
	case StatusUnreachable:
		return "unreachable"
	case StatusAmbiguous:
		return "ambiguous"
	default:
		return "unknown"
	}
}

// QueryResult 是一次最短路径查询的结果。
type QueryResult struct {
	// Status 查询最终状态。
	Status QueryStatus
	// Path 经过的对象标识序列（含起点与终点）。
	Path []ObjectID
	// Links 路径上的链接序列。
	Links []Link
	// Cost 路径总代价。
	Cost int64
	// Trace 查询过程中对每条候选链接的覆盖判定依据，按判定顺序排列。
	Trace []LinkDecision

	// snapshotVersion 查询使用的权限状态版本，仅供包内验证。
	snapshotVersion uint64
	// metric 本次查询的内部度量，不对调用者暴露。
	metric queryMetric
}

// Service 组合权限注册表与本体图，提供权限感知的路径查询。
type Service struct {
	registry *PermissionRegistry
	graph    *Graph
	// evalHook 仅供包内测试在覆盖判定过程中插入同步点。
	evalHook func()
}

// NewService 创建查询服务。
func NewService(registry *PermissionRegistry, graph *Graph) *Service {
	return &Service{registry: registry, graph: graph}
}

// pathState 是搜索过程中到达某对象的当前最优路径。
type pathState struct {
	cost  int64
	seq   []ObjectID
	links []Link
}

// compareSeq 按对象标识序列的字典序比较，前缀较短者更小。
func compareSeq(a, b []ObjectID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// better 判断候选路径是否优于当前最优：代价更小，或代价相同且序列字典序更小。
func better(cand, best pathState) bool {
	if cand.cost != best.cost {
		return cand.cost < best.cost
	}
	return compareSeq(cand.seq, best.seq) < 0
}

// stateHeap 是按（代价, 序列字典序）排序的最小堆。
type stateHeap []pathState

func (h stateHeap) Len() int { return len(h) }

func (h stateHeap) Less(i, j int) bool { return better(h[i], h[j]) }

func (h stateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *stateHeap) Push(x any) { *h = append(*h, x.(pathState)) }

func (h *stateHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// sameState 判断堆顶取出的条目是否仍是当前最优（非过期条目）。
func sameState(a, b pathState) bool {
	return a.cost == b.cost && compareSeq(a.seq, b.seq) == 0
}

// ShortestPath 查询从 from 到 to 的最短可遍历路径。
//
// 判定次序：权限主体标识非法直接报错；之后每条候选链接按
// 链接类型层 > 对象类型层（两端拒绝优先合并）> 默认拒绝 的次序判定。
// 查询在发起时刻加载一次权限快照，整个计算过程不感知后续变更。
func (s *Service) ShortestPath(principal PrincipalID, from, to ObjectID) (QueryResult, error) {
	if err := ValidatePrincipalID(principal); err != nil {
		return QueryResult{}, err
	}
	fromType, ok := s.graph.objectType(from)
	if !ok {
		return QueryResult{}, fmt.Errorf("%w: %q", ErrUnknownObject, string(from))
	}
	if _, ok := s.graph.objectType(to); !ok {
		return QueryResult{}, fmt.Errorf("%w: %q", ErrUnknownObject, string(to))
	}

	snap := s.registry.load()
	result := QueryResult{snapshotVersion: snap.version}
	eval := newEvaluator(snap, s.registry.policy, principal, &result.metric)
	eval.hook = s.evalHook

	if from == to {
		result.Status = StatusReachable
		result.Path = []ObjectID{from}
		return result, nil
	}

	types := map[ObjectID]ObjectTypeID{from: fromType}
	typeOf := func(id ObjectID) ObjectTypeID {
		t, ok := types[id]
		if !ok {
			t, _ = s.graph.objectType(id)
			types[id] = t
		}
		return t
	}

	best := map[ObjectID]pathState{
		from: {cost: 0, seq: []ObjectID{from}},
	}
	pq := &stateHeap{{cost: 0, seq: []ObjectID{from}}}
	heap.Init(pq)

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(pathState)
		node := cur.seq[len(cur.seq)-1]
		if !sameState(cur, best[node]) {
			continue
		}
		if node == to {
			result.Status = StatusReachable
			result.Path = cur.seq
			result.Links = cur.links
			result.Cost = cur.cost
			return result, nil
		}
		for _, link := range s.graph.outgoing(node) {
			dec := eval.authorize(link, typeOf(link.From), typeOf(link.To))
			result.Trace = append(result.Trace, dec)
			switch dec.Decision {
			case DecisionAmbiguous:
				result.Status = StatusAmbiguous
				return result, nil
			case DecisionAllow:
				cand := pathState{
					cost:  cur.cost + link.Cost,
					seq:   append(append([]ObjectID(nil), cur.seq...), link.To),
					links: append(append([]Link(nil), cur.links...), link),
				}
				if curBest, ok := best[link.To]; !ok || better(cand, curBest) {
					best[link.To] = cand
					heap.Push(pq, cand)
				}
			}
		}
	}

	result.Status = StatusUnreachable
	return result, nil
}
