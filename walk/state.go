package walk

// 本文件定义有预算 BFS 的可续传状态。状态按「层」组织，配合 walk.go
// 的惰性展开，保证队列驻留量受预算约束且断点可恢复。

// Frame 是一个「展开器」：Node 已访问，Cursor 指向其下一条待考察出边。
type Frame struct {
	Node   string
	Cursor int
}

// Checkpoint 是 BFS 的完整状态快照（不含图本身）。
//
//	Cur     本层（正在展开的层）的有序展开器帧，游标单调前进；
//	Pending 已发现、尚未访问的节点，按 BFS 访问序排列；
//	Next    已访问节点对应的下层展开器帧，按访问序排列（换层时升为 Cur）；
//	Visited 全部已访问节点；Done 为 true 时表示遍历完成。
//
// 初始（预算 0）状态：Cur=[{start,0}]，Pending=[start]，Next 为空。
type Checkpoint struct {
	Cur     []Frame
	Pending []string
	Next    []Frame
	Visited []string
	Done    bool
}

// Initial 返回从 start 出发、尚未消费任何预算的续点。
func Initial(start string) Checkpoint {
	return Checkpoint{
		Cur:     []Frame{{Node: start}},
		Pending: []string{start},
		Visited: nil,
	}
}

// Result 是一次（可能是分段的）遍历的产出。
type Result struct {
	Visited []string // 本次新访问的节点（输出序列）
	Next    Checkpoint

	visitedCount  int
	peakQueue     int
	edgesExamined int
}

// Stats 返回三项非导出计数器的只读快照，供测试与审计断言。
func (r Result) Stats() (visitedCount, peakQueue, edgesExamined int) {
	return r.visitedCount, r.peakQueue, r.edgesExamined
}
