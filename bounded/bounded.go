// Package bounded 对 BFS 遍历施加 MaxDepth 与 Limit，并给出截断三态。
//
// 三态互斥，用哨兵错误表示（errors.Is 可区分）：
//   - Complete：返回 err == nil，自然遍历完所有可达节点；
//   - DepthCut：返回 ErrDepthCut，自然走完但存在因深度上限被剪的未展开节点；
//   - LimitCut：返回 ErrLimitCut，输出满 Limit 时仍有已发现未输出的节点。
//
// 判定依据（见 DESIGN.md 推导 2/3）：LimitCut 看「停止时刻队列剩余」而非
// 「已输出 == n」；DepthCut 看「深度 d 处是否还有未展开的边」而非「图里是否有环」。
package bounded

import (
	"errors"
	"fmt"

	"ontology/graph"
	"ontology/traverse"
)

// 截断哨兵错误；Complete 以 nil 表示。
var (
	ErrDepthCut = errors.New("bounded: traversal cut by max depth")
	ErrLimitCut = errors.New("bounded: traversal cut by limit")
)

// Option 配置一次受限遍历。
type Option func(*config)

type config struct {
	maxDepth int // 0 = 不限
	limit    int // 0 = 不限
}

// MaxDepth 限定深度（起始节点深度 0）；d <= 0 表示不限。
func MaxDepth(d int) Option { return func(c *config) { c.maxDepth = d } }

// Limit 限定返回节点数；n <= 0 表示不限。
func Limit(n int) Option { return func(c *config) { c.limit = n } }

// Result 是受限遍历结果。Err 为 nil / ErrDepthCut / ErrLimitCut 之一。
type Result struct {
	Nodes []string
	Err   error

	out        int // 输出节点数（非导出，不变量断言用）
	unexpanded int // 已发现（含 LimitCut 后继续扩散统计）但未输出的可达节点数
}

type item struct {
	name  string
	depth int
}

// Walk 从 start 出发按 dir 做受限 BFS。
func Walk(g *graph.Graph, start string, dir traverse.Direction, opts ...Option) (Result, error) {
	if !dir.Valid() {
		return Result{}, fmt.Errorf("bounded: invalid direction %d", dir)
	}
	if !g.Has(start) {
		return Result{}, fmt.Errorf("bounded: start node %q does not exist", start)
	}
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	visited := map[string]bool{start: true}
	queue := []item{{start, 0}}
	var deferred []item // 因深度被剪、已发现未输出的节点
	var res Result
	limitHit := false

	for len(queue) > 0 {
		if cfg.limit > 0 && res.out == cfg.limit {
			limitHit = true // 队列非空：存在已发现未输出节点
			break
		}
		n := queue[0]
		queue = queue[1:]
		res.Nodes = append(res.Nodes, n.name)
		res.out++
		for _, m := range neighbors(g, n.name, dir) {
			if visited[m] {
				continue
			}
			visited[m] = true
			if cfg.maxDepth > 0 && n.depth+1 > cfg.maxDepth {
				deferred = append(deferred, item{m, n.depth + 1})
				continue
			}
			queue = append(queue, item{m, n.depth + 1})
		}
	}

	if limitHit {
		res.Err = ErrLimitCut
	}
	// 只读扩散：从剩余队列与被深度剪掉的节点出发，统计所有尚未输出的
	// 可达节点，维持「输出 + 未展开 == 可达」不变量（环上每节点只贡献一次）。
	rest := make([]item, 0, len(queue)+len(deferred))
	rest = append(rest, queue...)
	rest = append(rest, deferred...)
	for len(rest) > 0 {
		n := rest[0]
		rest = rest[1:]
		res.unexpanded++
		for _, m := range neighbors(g, n.name, dir) {
			if !visited[m] {
				visited[m] = true
				rest = append(rest, item{m, n.depth + 1})
			}
		}
	}
	if limitHit {
		return res, nil
	}
	if res.unexpanded > 0 {
		res.Err = ErrDepthCut
	}
	return res, nil
}

func neighbors(g *graph.Graph, n string, dir traverse.Direction) []string {
	switch dir {
	case traverse.Out:
		return g.Out(n)
	case traverse.In:
		return g.In(n)
	default:
		out := g.Out(n)
		seen := make(map[string]bool, len(out))
		merged := make([]string, 0, len(out)+len(g.In(n)))
		for _, m := range out {
			seen[m] = true
			merged = append(merged, m)
		}
		for _, m := range g.In(n) {
			if !seen[m] {
				merged = append(merged, m)
			}
		}
		return merged
	}
}
