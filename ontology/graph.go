// Package ontology 提供提交图的登记、世代号查询与合并基选择能力。
//
// 图中所有边由子指向父（祖先方向）。登记顺序保证父先于子，因此图无环。
package ontology

import (
	"container/heap"
	"sort"
	"sync"
)

// commitNode 保存一个已登记提交的父列表与世代号。
type commitNode struct {
	id      string
	parents []string
	gen     int
}

// Graph 是并发安全的提交图。零值不可用，请使用 NewGraph 构造。
type Graph struct {
	mu     sync.RWMutex
	nodes  map[string]*commitNode
	visits int // 非导出计数器：记录最近一次 MergeBases 实际遍历到的提交数
}

// NewGraph 创建一个空提交图。
func NewGraph() *Graph {
	return &Graph{nodes: make(map[string]*commitNode)}
}

// Commit 登记一个提交。
// 校验严格按以下顺序只报第一个错误：id 为空、id 已登记、父列表超过 8 个、
// 父列表含重复（取最小下标处）、父未登记（取最小下标处）。
// 被拒绝时图不发生任何改变。
func (g *Graph) Commit(id string, parents []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id == "" {
		return newError(ReasonEmptyID, id, -1)
	}
	if _, ok := g.nodes[id]; ok {
		return newError(ReasonDuplicateID, id, -1)
	}
	if len(parents) > 8 {
		return newError(ReasonTooManyParents, id, -1)
	}
	seen := make(map[string]struct{}, len(parents))
	for i, p := range parents {
		if _, dup := seen[p]; dup {
			return newError(ReasonDuplicateParent, p, i)
		}
		seen[p] = struct{}{}
		if _, ok := g.nodes[p]; !ok {
			return newError(ReasonParentNotRegistered, p, i)
		}
	}

	gen := 1
	for _, p := range parents {
		if pg := g.nodes[p].gen + 1; pg > gen {
			gen = pg
		}
	}
	stored := make([]string, len(parents))
	copy(stored, parents)
	g.nodes[id] = &commitNode{id: id, parents: stored, gen: gen}
	return nil
}

// Gen 返回提交的世代号。
func (g *Graph) Gen(id string) (int, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return 0, newError(ReasonGenUnknownID, id, -1)
	}
	return n.gen, nil
}

// IsAncestor 判定 a 是否为 b 的祖先（含相等）。
func (g *Graph) IsAncestor(a, b string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.nodes[a]; !ok {
		return false, newError(ReasonIsAncestorUnknownA, a, -1)
	}
	nb, ok := g.nodes[b]
	if !ok {
		return false, newError(ReasonIsAncestorUnknownB, b, -1)
	}
	// 世代号剪枝：祖先的世代号严格小于后代（除自身相等）。
	if g.nodes[a].gen > nb.gen {
		return false, nil
	}
	return g.containsLocked(a, b), nil
}

// MergeBases 返回 a 与 b 的全部合并基，按 id 字节序升序。
func (g *Graph) MergeBases(a, b string) ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if _, ok := g.nodes[a]; !ok {
		return nil, newError(ReasonMergeBasesUnknownA, a, -1)
	}
	if _, ok := g.nodes[b]; !ok {
		return nil, newError(ReasonMergeBasesUnknownB, b, -1)
	}

	bases := g.mergeBasesLocked(a, b)
	sort.Strings(bases)
	return bases, nil
}

// containsLocked 在调用方已持读锁的前提下，从 start 沿父边向上搜索目标 target。
// 调用方需已保证 target 的世代号不大于 start。
func (g *Graph) containsLocked(target, start string) bool {
	if target == start {
		return true
	}
	targetGen := g.nodes[target].gen
	stack := []string{start}
	visited := map[string]struct{}{start: {}}
	for len(stack) > 0 {
		n := len(stack) - 1
		cur := stack[n]
		stack = stack[:n]
		for _, p := range g.nodes[cur].parents {
			// 世代号剪枝：世代号小于 target 的父边不可能到达 target。
			if g.nodes[p].gen < targetGen {
				continue
			}
			if p == target {
				return true
			}
			if _, seen := visited[p]; !seen {
				visited[p] = struct{}{}
				stack = append(stack, p)
			}
		}
	}
	return false
}

// side 用位掩码记录某提交当前被哪一侧的可达性标记命中。
const (
	sideA uint8 = 1 << iota
	sideB
)

// genHeap 是按世代号降序排列的小顶堆（世代号取负），同世代按 id 升序以保证确定性。
type genHeap []heapEntry

type heapEntry struct {
	id  string
	gen int
}

func (h genHeap) Len() int { return len(h) }
func (h genHeap) Less(i, j int) bool {
	if h[i].gen != h[j].gen {
		return h[i].gen > h[j].gen
	}
	return h[i].id < h[j].id
}
func (h genHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *genHeap) Push(x any)   { *h = append(*h, x.(heapEntry)) }
func (h *genHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// mergeBasesLocked 实现带世代号剪枝的双向洪泛（类似 Git 的 merge-base 算法）。
//
// 从 a、b 两端同时沿父边传播可达标记，世代号大的提交先出堆；父的世代号
// 严格小于子，因此标记只向更老的世代传播。提交首次同时带两侧标记时记为
// 候选公共祖先，并停止从该点继续向上洪泛（其祖先经由该点的可达性已被它
// 代表），从而保证遍历规模与汇聚点之下的历史总长度无关。
// 标记仍可能经由不经过汇聚点的旁路抵达更老的公共祖先，这些候选一定是
// 某个世代更大的候选的祖先，故最后按定义剔除“以其他候选为后代”的候选。
func (g *Graph) mergeBasesLocked(a, b string) []string {
	g.visits = 0
	flags := make(map[string]uint8)
	inHeap := make(map[string]struct{})
	h := &genHeap{}
	heap.Init(h)

	push := func(id string, flag uint8) {
		before := flags[id]
		after := before | flag
		flags[id] = after
		// 只有携带“新标记”时才需要重新入队处理；已经在堆里的提交会在
		// 弹出时读到最新标记，无需重复入队。
		if before != after {
			if _, queued := inHeap[id]; !queued {
				inHeap[id] = struct{}{}
				heap.Push(h, heapEntry{id: id, gen: g.nodes[id].gen})
			}
		}
	}
	push(a, sideA)
	push(b, sideB)

	var bases []string
	for h.Len() > 0 {
		entry := heap.Pop(h).(heapEntry)
		cur := entry.id
		delete(inHeap, cur)
		mark := flags[cur]
		g.visits++

		if mark == sideA|sideB {
			bases = append(bases, cur)
			// 汇聚点代表其全部祖先，停止洪泛，避免沿长历史上溯。
			continue
		}

		// 世代号剪枝：父世代号严格小于当前提交，只向更老的世代传播。
		for _, p := range g.nodes[cur].parents {
			if g.nodes[p].gen >= g.nodes[cur].gen {
				continue
			}
			push(p, mark)
		}
	}

	// 按定义取极大元：候选 x 若是候选集合中任一其他提交 y 的祖先，
	// 则 x 被支配，剔除（containsLocked(x, y) 判定 x 自 y 可达）。
	// 候选数量很少，O(k^2) 判定足够且不依赖处理顺序。
	var maximal []string
	for i, x := range bases {
		dominated := false
		for j, y := range bases {
			if i == j {
				continue
			}
			if g.nodes[x].gen <= g.nodes[y].gen && g.containsLocked(x, y) {
				dominated = true
				break
			}
		}
		if !dominated {
			maximal = append(maximal, x)
		}
	}
	sort.Strings(maximal)
	return maximal
}

// lastMergeBaseVisits 返回最近一次 MergeBases 弹出处理的不同提交数，
// 供测试验证世代号剪枝效果。
func (g *Graph) lastMergeBaseVisits() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.visits
}
