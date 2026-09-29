package spill

// LogEntry 是下游日志中的一行已提交记录。
type LogEntry struct {
	TxID      uint64
	CommitSeq uint64
	Row       Row
}

// DownstreamLog 是下游已提交事务日志。只有提交事务的行才会出现，
// 行序与该事务的追加顺序一致；事务之间按提交先后排列。
type DownstreamLog struct{}

func newDownstreamLog() *DownstreamLog { return &DownstreamLog{} }

// Entries 返回日志快照拷贝。
func (l *DownstreamLog) Entries() []LogEntry { return nil }

func (l *DownstreamLog) appendTx(txID uint64, commitSeq uint64, rows []Row) {}
