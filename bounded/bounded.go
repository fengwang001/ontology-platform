// Package bounded 对遍历施加 MaxDepth / Limit 截断，并给出三态结果：
// Complete（Err == nil）/ DepthCut / LimitCut。语义推导见 DESIGN.md。
package bounded

import (
	"errors"

	"ontology/graph"
	"ontology/traverse"
)

// 截断三态中的两个截断态哨兵错误；Complete 以 Err == nil 表示。
var (
	ErrLimitCut = errors.New("bounded: limit cut")
	ErrDepthCut = errors.New("bounded: depth cut")
)

// Result 是一次截断遍历的结果。Err 三态互斥：
// nil 即 Complete，否则 errors.Is 可区分 ErrLimitCut / ErrDepthCut。
type Result struct {
	Nodes []string
	Err   error
}

// Walker 按固定方向与上界做截断遍历。
type Walker struct {
	g        *graph.Graph
	dir      traverse.Dir
	maxDepth int // <= 0 表示不限深度
	limit    int // <= 0 表示不限数量

	// 非导出计数器：证明「输出数 + 未展开数 == 可达数」不变量。
	output     int
	unexpanded int
}

type Option func(*Walker)

// MaxDepth 限定深度（起点深度 0）；d <= 0 表示不限。
func MaxDepth(d int) Option { return func(w *Walker) { w.maxDepth = d } }

// Limit 限定输出节点数；n <= 0 表示不限。
func Limit(n int) Option { return func(w *Walker) { w.limit = n } }

func New(g *graph.Graph, dir traverse.Dir, opts ...Option) *Walker {
	w := &Walker{g: g, dir: dir}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Counters 返回最近一次 Walk 的「输出数, 未展开数」，
// 供外部断言 输出 + 未展开 == 可达节点数（同深度上界下）。
func (w *Walker) Counters() (output, unexpanded int) {
	return w.output, w.unexpanded
}

type item struct {
	name  string
	depth int
}

// Walk 从 start 出发做带截断的 BFS。返回值的 error 是参数校验错误；
// 截断三态在 Result.Err 上。
func (w *Walker) Walk(start string) (Result, error) {
	if !w.dir.Valid() {
		return Result{}, traverse.ErrInvalidDir
	}
	if !w.g.Has(start) {
		return Result{}, graph.ErrNodeNotFound
	}
	w.output, w.unexpanded = 0, 0

	seen := map[string]bool{start: true}
	queue := []item{{start, 0}}
	var nodes []string
	depthCut := false

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		nodes = append(nodes, cur.name)

		for _, nb := range traverse.Neighbors(w.g, cur.name, w.dir) {
			if seen[nb] {
				continue // 含环回边：指向已发现节点，不构成截断
			}
			if w.maxDepth > 0 && cur.depth+1 > w.maxDepth {
				// 深度 d 处仍存在指向未发现节点的边 → DepthCut。
				// BFS 按深度非降处理，此时更浅路径已全部展开，判定无遗漏。
				depthCut = true
				continue
			}
			seen[nb] = true
			queue = append(queue, item{nb, cur.depth + 1})
		}

		// LimitCut 判定：输出满 limit 且仍有已发现未展开的节点。
		// 可达数恰好 == limit 时队列已空，落 Complete 而非 LimitCut。
		if w.limit > 0 && len(nodes) >= w.limit && len(queue) > 0 {
			w.output = len(nodes)
			w.unexpanded = w.drain(queue, seen)
			return Result{Nodes: nodes, Err: ErrLimitCut}, nil
		}
	}

	w.output = len(nodes)
	if depthCut {
		return Result{Nodes: nodes, Err: ErrDepthCut}, nil
	}
	return Result{Nodes: nodes}, nil
}

// drain 在 limit 截断后继续不计输出的 BFS（同一去重集、同一深度上界），
// 把剩余可达节点全部计入未展开数；环上每个节点只贡献一次。
func (w *Walker) drain(queue []item, seen map[string]bool) int {
	count := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		count++
		for _, nb := range traverse.Neighbors(w.g, cur.name, w.dir) {
			if seen[nb] {
				continue
			}
			if w.maxDepth > 0 && cur.depth+1 > w.maxDepth {
				continue
			}
			seen[nb] = true
			queue = append(queue, item{nb, cur.depth + 1})
		}
	}
	return count
}
