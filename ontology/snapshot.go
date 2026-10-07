package ontology

// snapshotStride 快照步长：每积累 stride 条提交物化一次全量快照。
// 回放代价因此上界为 O(log(快照数) + stride + 当时存活链接数)，
// 与链接类型累计发生的事实总量无关（见设计文档第 4 节）。
const snapshotStride = 64

// snapshot 某一记录时刻的全量链接状态，物化后不可变。
type snapshot struct {
	at     int64               // 对应的记录时刻（最后一条被包含事实的 RecordedAt）
	count  int                 // 已包含的事实条数
	states map[pair]*pairState // 不可变；回放时克隆后再应用增量
}

// cloneStates 深拷贝状态表，供回放路径在副本上应用增量事实。
func cloneStates(src map[pair]*pairState) map[pair]*pairState {
	dst := make(map[pair]*pairState, len(src))
	for p, ps := range src {
		c := ps.copy()
		dst[p] = &c
	}
	return dst
}

// materialize 从上一快照出发应用 facts[from:to] 生成新快照。
func materialize(prev *snapshot, facts []Fact, from, to int, def LinkTypeDef) snapshot {
	var states map[pair]*pairState
	if prev != nil {
		states = cloneStates(prev.states)
	} else {
		states = make(map[pair]*pairState)
	}
	for i := from; i < to; i++ {
		applyFact(states, facts[i], def)
	}
	return snapshot{at: facts[to-1].RecordedAt, count: to, states: states}
}
