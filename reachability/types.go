package reachability

// Pair 是一个有序点对 (From -> To)。
type Pair struct {
	From string
	To   string
}

// PairWitness 附带一条证明可达的见证路径（顶点序列，长度至少为 2，
// 自环为 [v, v]；恢复判定中的自可达可能给出更短的环）。
type PairWitness struct {
	Pair
	Path []string
}

// ChangeResult 描述一次成功增删边操作带来的变化，作为判定依据。
type ChangeResult struct {
	// Op 为 "add_edge" 或 "remove_edge"。
	Op   string
	Edge Pair
	// MultiplicityBefore/After 是操作前后该边的重数。
	MultiplicityBefore uint64
	MultiplicityAfter  uint64
	// StructureChanged 表示边是否在 “不存在 <-> 存在” 之间跃迁，
	// 仅跃迁时可达集合才可能变化。
	StructureChanged bool
	// AddedPairs：加边后新增的可达点对及其见证路径。
	AddedPairs []PairWitness
	// RemovedPairs：删边时先移除的“可能受影响”点对中最终未恢复的部分。
	RemovedPairs []Pair
	// RestoredPairs：先移除、再经朴素遍历重新推导加回的点对及新见证路径。
	RestoredPairs []PairWitness

	Before *Snapshot
	After  *Snapshot
}
