// Package api 组装 graph + traverse + bounded，是唯一对外入口。
package api

import (
	"fmt"

	"ontology/bounded"
	"ontology/graph"
	"ontology/traverse"
)

// Direction 为 traverse.Direction 的别名，调用方只需导入 api。
type Direction = traverse.Direction

// 方向常量。
const (
	Out  = traverse.Out
	In   = traverse.In
	Both = traverse.Both
)

// 截断三态哨兵错误（Complete 以 Response.Err == nil 表示）。
var (
	ErrDepthCut = bounded.ErrDepthCut
	ErrLimitCut = bounded.ErrLimitCut
)

// Request 是一次遍历请求。MaxDepth / Limit 为 0 表示不施加该约束，
// 负数是非法参数。
type Request struct {
	Graph    *graph.Graph
	Start    string
	Dir      Direction
	MaxDepth int
	Limit    int
}

// Response 是遍历结果。Err 为 nil（Complete）/ ErrDepthCut / ErrLimitCut
// 之一，可用 errors.Is 判定。
type Response struct {
	Nodes []string
	Err   error
}

// Traverse 校验参数并执行受限遍历。
func Traverse(req Request) (Response, error) {
	if req.Graph == nil {
		return Response{}, fmt.Errorf("api: nil graph")
	}
	if !req.Dir.Valid() {
		return Response{}, fmt.Errorf("api: invalid direction %d", int(req.Dir))
	}
	if req.MaxDepth < 0 {
		return Response{}, fmt.Errorf("api: MaxDepth %d < 0", req.MaxDepth)
	}
	if req.Limit < 0 {
		return Response{}, fmt.Errorf("api: Limit %d < 0", req.Limit)
	}
	if !req.Graph.Has(req.Start) {
		return Response{}, fmt.Errorf("api: start node %q does not exist", req.Start)
	}
	res, err := bounded.Walk(req.Graph, req.Start, req.Dir,
		bounded.MaxDepth(req.MaxDepth), bounded.Limit(req.Limit))
	if err != nil {
		return Response{}, err
	}
	return Response{Nodes: res.Nodes, Err: res.Err}, nil
}
