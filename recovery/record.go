// Package recovery 实现 ARIES 风格恢复分析阶段（Analyze）的日志与计算器。
package recovery

// RecordType 日志记录类型。
type RecordType int

const (
	Update    RecordType = iota // Update(txn, page)
	Commit                      // Commit(txn)
	Abort                       // Abort(txn)
	End                         // End(txn)
	BeginCkpt                   // BeginCkpt
	EndCkpt                     // EndCkpt(begin, dpt, att)
	PageFlush                   // PageFlush(page)
)

func (t RecordType) String() string {
	switch t {
	case Update:
		return "Update"
	case Commit:
		return "Commit"
	case Abort:
		return "Abort"
	case End:
		return "End"
	case BeginCkpt:
		return "BeginCkpt"
	case EndCkpt:
		return "EndCkpt"
	case PageFlush:
		return "PageFlush"
	}
	return "Unknown"
}

// TxnStatus 事务状态。
type TxnStatus int

const (
	Running   TxnStatus = iota // 运行中
	Committed                  // 已提交
	Aborting                   // 回滚中
)

func (s TxnStatus) String() string {
	switch s {
	case Running:
		return "Running"
	case Committed:
		return "Committed"
	case Aborting:
		return "Aborting"
	}
	return "Unknown"
}

// AttSnapshotEntry EndCkpt 快照中单个事务的（状态, 最后 LSN）。
type AttSnapshotEntry struct {
	Status  TxnStatus
	LastLSN int64
}

// Record 一条日志记录的内容（不含 LSN，LSN 由 Append 指定）。
type Record struct {
	Type RecordType
	Txn  int // Update/Commit/Abort/End 使用
	Page int // Update/PageFlush 使用

	// 以下字段仅 EndCkpt 使用。
	Begin int64                    // 对应 BeginCkpt 的 LSN
	DPT   map[int]int64            // 页 -> recLSN 快照
	ATT   map[int]AttSnapshotEntry // 事务 -> (状态, 最后 LSN) 快照
}

// DirtyPage 脏页表项。
type DirtyPage struct {
	Page   int
	RecLSN int64
}

// AttEntry 活跃事务表项。
type AttEntry struct {
	Txn     int
	Status  TxnStatus
	LastLSN int64
}

// Analysis Analyze 的结果。DPT 按页升序，ATT 按事务升序，Failed 升序。
type Analysis struct {
	NeedRedo bool        // 脏页表非空时才需要重做
	RedoLSN  int64       // 重做起点 = 脏页表最小 recLSN（NeedRedo 为 false 时无意义）
	DPT      []DirtyPage // 脏页表，按页升序
	ATT      []AttEntry  // 活跃事务表，按事务升序
	Failed   []int       // 失败事务集合（状态为运行或回滚中），升序
}
