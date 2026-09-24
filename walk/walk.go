// Package walk 在给定访问预算下做确定性的广度优先遍历。
// 预算以「访问（产出）的节点数」计量；节点出边以游标方式分段展开，
// 使队列驻留量与预算同阶而不是与图规模同阶。
package walk

import (
	"errors"

	"ontology/graph"
)

// ErrMissingNode 表示起点或队列项引用了图中不存在的节点。
var ErrMissingNode = errors.New("walk: referenced node does not exist in graph")

// Frame 是队列项：一个待访问节点及其出边扫描游标。
type Frame struct {
	Node   string
	Cursor int
}

type nodeError struct {
	node string
}

func (e *nodeError) Error() string        { return ErrMissingNode.Error() + ": " + e.node }
func (e *nodeError) Is(target error) bool { return target == ErrMissingNode }

// MissingNode 从错误中取出缺失的节点 ID；ok 为 false 表示不是该类错误。
func MissingNode(err error) (string, bool) {
	var e *nodeError
	if errors.As(err, &e) {
		return e.node, true
	}
	return "", false
}

func missingError(node string) error { return &nodeError{node: node} }

// State 是可续传的遍历机器状态（即续点的内存形态）。
type State struct {
	Queue []Frame
	// Visited 是已产出节点集合；Discovered 是 Visited 的超集（含队列中节点）。
	// 二者分开，分段续传才不会把"上一段预占入队"的节点误当作已访问。
	Visited    map[string]struct{}
	Discovered map[string]struct{}
	Done       bool

	// 非导出资源计数器；每次 Run 在该副本上累计，跨续传单调。
	visited       int
	peakQueue     int
	edgesExamined int
}

// CountVisited 返回累计访问的节点数。
func (s *State) CountVisited() int { return s.visited }

// CountPeakQueue 返回队列驻留长度的历史峰值。
func (s *State) CountPeakQueue() int { return s.peakQueue }

// CountEdgesExamined 返回累计被考察的边数。
func (s *State) CountEdgesExamined() int { return s.edgesExamined }

// Initial 返回从 start 出发的初始续点。
func Initial(start string) *State {
	return &State{
		Queue:      []Frame{{Node: start}},
		Visited:    map[string]struct{}{},
		Discovered: map[string]struct{}{start: {}},
	}
}

// Clone 深拷贝状态，使多次（并发）续传互不串台。
func (s *State) Clone() *State {
	cp := &State{
		Done:          s.Done,
		visited:       s.visited,
		peakQueue:     s.peakQueue,
		edgesExamined: s.edgesExamined,
		Visited:       make(map[string]struct{}, len(s.Visited)),
		Discovered:    make(map[string]struct{}, len(s.Discovered)),
		Queue:         append([]Frame(nil), s.Queue...),
	}
	for id := range s.Visited {
		cp.Visited[id] = struct{}{}
	}
	for id := range s.Discovered {
		cp.Discovered[id] = struct{}{}
	}
	return cp
}

func (s *State) notePeakLen(n int) {
	if n > s.peakQueue {
		s.peakQueue = n
	}
}

// Run 从状态 s 出发，最多访问 budget 个节点，返回本次访问序列。
// Run 不修改 s：它在副本上推进并返回新状态，满足纯函数语义。
func Run(g *graph.Graph, s *State, budget int) ([]string, *State, error) {
	if budget < 0 {
		budget = 0
	}
	next := s.Clone()
	out := make([]string, 0, budget)
	if next.Done {
		return out, next, nil
	}
	if budget == 0 {
		return out, next, nil
	}

	rem := budget
	q := next.Queue
	for rem > 0 && len(q) > 0 {
		head := q[0]
		q = q[1:]
		if !g.Has(head.Node) {
			return nil, nil, missingError(head.Node)
		}
		out = append(out, head.Node)
		next.Visited[head.Node] = struct{}{}
		next.visited++
		rem--

		// 有界展开：未产出队列项的驻留数始终 <= budget+1。预算未耗尽时
		// 继续推进（前瞻入队的项随后就会被产出）；仅当预算恰好耗尽且已
		// 前瞻入队一项时才挂起。挂起时游标停在第一条未入队边，续传重扫，
		// 分段拼接顺序与一次遍历逐元素一致，每条边至多被考察一次。
		neighbors := g.Neighbors(head.Node)
		start := head.Cursor
		cursor := start
		for cursor < len(neighbors) {
			to := neighbors[cursor]
			if _, visited := next.Visited[to]; visited {
				cursor++
				continue
			}
			if _, queued := next.Discovered[to]; queued {
				cursor++
				continue
			}
			if len(q) > rem {
				next.edgesExamined += cursor - start
				next.notePeakLen(len(q) + 1)
				next.Queue = append([]Frame{{Node: head.Node, Cursor: cursor}}, q...)
				return out, next, nil
			}
			cursor++
			next.Discovered[to] = struct{}{}
			q = append(q, Frame{Node: to})
		}
		next.edgesExamined += cursor - start
		next.notePeakLen(len(q) + 1)
	}
	next.Queue = q
	if len(next.Queue) == 0 {
		next.Done = true
	}
	return out, next, nil
}
