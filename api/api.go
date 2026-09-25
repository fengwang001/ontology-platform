// Package api 是对象图遍历器的唯一对外入口，组装 graph + traverse + bounded。
package api

import (
	"errors"
	"fmt"

	"ontology/bounded"
	"ontology/graph"
	"ontology/traverse"
)

// 截断三态哨兵错误（重导出，便于调用方只依赖 api 包）。
// Complete 以 Response.Err == nil 表示。
var (
	ErrLimitCut = bounded.ErrLimitCut
	ErrDepthCut = bounded.ErrDepthCut
)

var (
	ErrNilGraph   = errors.New("api: graph is nil")
	ErrNoStart    = errors.New("api: start node does not exist")
	ErrInvalidDir = errors.New("api: invalid direction")
)

// Request 是一次遍历请求。
// MaxDepth <= 0 表示不限深度；Limit <= 0 表示不限数量（零值即无界）。
type Request struct {
	Graph    *graph.Graph
	Start    string
	Dir      traverse.Dir
	MaxDepth int
	Limit    int
}

// Response 是遍历结果。Err 三态互斥：nil=Complete，
// 否则可 errors.Is(ErrLimitCut) / errors.Is(ErrDepthCut) 区分。
type Response struct {
	Nodes []string
	Err   error
}

// HasCycle 报告图中是否存在环（三色 DFS，菱形不误报）。
func HasCycle(g *graph.Graph) (bool, error) {
	if g == nil {
		return false, ErrNilGraph
	}
	return g.HasCycle(), nil
}

// Traverse 是对外唯一遍历入口：先校验参数，再做带截断的 BFS。
func Traverse(req Request) (Response, error) {
	if req.Graph == nil {
		return Response{}, ErrNilGraph
	}
	if !req.Dir.Valid() {
		return Response{}, fmt.Errorf("%w: %v", ErrInvalidDir, req.Dir)
	}
	if !req.Graph.Has(req.Start) {
		return Response{}, fmt.Errorf("%w: %q", ErrNoStart, req.Start)
	}
	w := bounded.New(req.Graph, req.Dir,
		bounded.MaxDepth(req.MaxDepth), bounded.Limit(req.Limit))
	res, err := w.Walk(req.Start)
	if err != nil {
		return Response{}, err
	}
	return Response{Nodes: res.Nodes, Err: res.Err}, nil
}
