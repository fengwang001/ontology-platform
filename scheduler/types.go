package scheduler

// Transaction 描述按提交顺序到达的一个事务。
// Seq 为事务序号，必须从 1 开始连续递增。
// WriteKeys 为写键集合，非空且不得包含空键；重复键只算一个。
// ReadKeys 为读键集合，仅作审计记录，不参与任何依赖判定。
type Transaction struct {
	Seq       int
	ReadKeys  []string
	WriteKeys []string
}

// Round 表示一次分批调度的一轮。
// 同一轮内的事务互不依赖，可以真正并发回放。
type Round struct {
	Index int
	Seqs  []int
}

// ReplayResult 是一次回放的结果。
// State 为每个键最终写入它的事务序号，与按提交顺序串行回放完全一致。
// Rounds 为本次回放实际采用的分批方案。
type ReplayResult struct {
	Rounds []Round
	State  map[string]int
}

// SelfCheckReport 是自检报告。
type SelfCheckReport struct {
	OK              bool
	Parallelism     int
	RoundCount      int
	TransactionSeqs []int
}
