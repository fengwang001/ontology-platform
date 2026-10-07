package traversal

import (
	"context"
)

// Logger 记录每次遍历的输入、每条路径的终态分类与判定所依据的祖先序列。
// 实现必须是并发安全的。nil 记录器表示不输出日志。
type Logger interface {
	LogEntry(entry LogEntry)
}

// PathLog 是单条路径的日志记录。
type PathLog struct {
	PathIndex        int
	Nodes            []ObjectID
	Depth            int
	Status           TerminalStatus
	CycleAncestors   []ObjectID
	CycleRepeated    ObjectID
	CycleAncestorIdx int
}

// LogEntry 是一次遍历请求的完整日志条目。
type LogEntry struct {
	Start           ObjectID
	Directions      map[LinkTypeID]Direction
	MaxDepth        int
	Accepted        bool
	Error           string
	SnapshotVersion uint64
	Paths           []PathLog
	Stats           Stats
}

// Service 提供图遍历服务。
type Service struct {
	graph  *Graph
	logger Logger
}

// NewService 创建遍历服务。
func NewService(graph *Graph, logger Logger) *Service {
	return &Service{graph: graph, logger: logger}
}

// Traverse 执行一次遍历。
func (s *Service) Traverse(ctx context.Context, req TraversalRequest) (*TraversalResult, error) {
	// 开始前已取消/超时：直接拒绝，不获取遍历结果、不产生部分路径。
	// 遍历本身在不可变快照上完成，不支持中途打断，避免给出
	// 「半条快照」式结果。
	if err := ctx.Err(); err != nil {
		if s.logger != nil {
			s.logger.LogEntry(buildLogEntry(req, 0, nil, err))
		}
		return nil, err
	}
	// 先原子获取开始时刻的不可变快照；整个遍历只引用该快照，
	// 期间发生的任何链接增删都落在更新的快照上，不会混入本次结果。
	snap := s.graph.loadSnapshot()
	result, err := TraverseSnapshot(snap, req)
	if s.logger != nil {
		s.logger.LogEntry(buildLogEntry(req, snap.version, result, err))
	}
	return result, err
}

// TraverseSnapshot 在指定快照上执行遍历（主要供测试对照与快照一致性验证使用）。
func TraverseSnapshot(snap *snapshot, req TraversalRequest) (*TraversalResult, error) {
	return traverse(snap, req)
}

func buildLogEntry(req TraversalRequest, version uint64, result *TraversalResult, err error) LogEntry {
	entry := LogEntry{
		Start:      req.Start,
		Directions: cloneDirections(req.Directions),
		MaxDepth:   req.MaxDepth,
		Accepted:   err == nil,
	}
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.SnapshotVersion = version
	entry.Stats = result.Stats
	entry.Paths = make([]PathLog, 0, len(result.Paths))
	for i, p := range result.Paths {
		pl := PathLog{
			PathIndex: i,
			Nodes:     append([]ObjectID(nil), p.Nodes...),
			Depth:     p.Depth,
			Status:    p.Status,
		}
		if p.Cycle != nil {
			// 据以判定的祖先序列随路径一并记录，便于审计复现。
			pl.CycleAncestors = append([]ObjectID(nil), p.Cycle.AncestorSequence...)
			pl.CycleRepeated = p.Cycle.RepeatedObject
			pl.CycleAncestorIdx = p.Cycle.AncestorIndex
		}
		entry.Paths = append(entry.Paths, pl)
	}
	return entry
}

func cloneDirections(in map[LinkTypeID]Direction) map[LinkTypeID]Direction {
	if len(in) == 0 {
		return nil
	}
	out := make(map[LinkTypeID]Direction, len(in))
	for t, d := range in {
		out[t] = d
	}
	return out
}
