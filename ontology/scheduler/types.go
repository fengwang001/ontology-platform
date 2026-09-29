package scheduler

import "time"

// MaxTransactions 是单个调度器可接受的事务总数上限。
const MaxTransactions = 100_000

// Transaction 描述一个按提交顺序到达的事务。
//
// Seq 为事务序号，必须从 1 开始严格连续递增。ReadKeys 为读键集合，
// 仅用于审计记录，不参与依赖判定也不会在回放时读取任何值。WriteKeys
// 为写键集合，同一事务内重复出现的键只计一次；WriteValues 给出每个
// 写键在回放时写入的值，未在其中出现的写键写入确定性派生值
// "tx<seq>:<key>"，从而保证回放结果可复现。
type Transaction struct {
	Seq         int
	ReadKeys    []string
	WriteKeys   []string
	WriteValues map[string]string
}

// Executor 执行单个事务，返回该事务对其每个写键写入的最终值。
//
// 同一调度轮次内的多个事务会在不同 goroutine 上并发调用 Executor；
// 实现必须对互不相交的键集合保持线程安全。
type Executor func(tx Transaction) (writes map[string]string, err error)

// TransactionPlan 是单个事务的深度与调度轮次信息。
type TransactionPlan struct {
	Seq       int
	Depth     int
	Round     int
	DependsOn []int
	Reason    string
}

// RoundPlan 描述一轮调度。
type RoundPlan struct {
	Round    int
	TxSeqs   []int
	Parallel bool
}

// TxExecution 记录单个事务的回放结果。
type TxExecution struct {
	Seq     int
	Round   int
	Writes  map[string]string
	Started time.Time
	Ended   time.Time
}

// ReplayResult 是一次完整回放的结果。
type ReplayResult struct {
	State      map[string]string
	Rounds     []RoundPlan
	Executions []TxExecution
}
