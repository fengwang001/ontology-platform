package ontology

// Message 是一条属性变更事件。
//
// Seq 由分发器在 Publish 时分配，全局单调递增（从 1 开始）。
// 订阅者收到的消息序号严格递增，允许存在缺口；
// 缺口中的序号就是被该订阅者丢弃的消息。
type Message struct {
	Seq      uint64
	Entity   string
	Property string
	Value    any
}

// Gaps 返回 seqs 中相邻序号之间的缺口区间（含两端），
// 即被丢弃消息的序号范围。seqs 必须严格递增。
//
// 例如 seqs 为 [1 2 5 9] 时返回 [{3,4}, {6,8}]。
func Gaps(seqs []uint64) [][2]uint64 {
	var gaps [][2]uint64
	for i := 1; i < len(seqs); i++ {
		prev, cur := seqs[i-1], seqs[i]
		if cur > prev+1 {
			gaps = append(gaps, [2]uint64{prev + 1, cur - 1})
		}
	}
	return gaps
}

// GapCount 返回 seqs 中缺口包含的序号总数。
func GapCount(seqs []uint64) uint64 {
	var n uint64
	for _, g := range Gaps(seqs) {
		n += g[1] - g[0] + 1
	}
	return n
}
