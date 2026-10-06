package scrub

// Time 是单调的逻辑时钟刻度（非负整数），由调用方注入。
type Time int64

// Outcome 是一次巡检仲裁的终局分类。
type Outcome int

const (
	// OutcomeNoRepair 全部副本已与权威一致，无需写入。
	OutcomeNoRepair Outcome = iota
	// OutcomeRepaired 所有需修复副本均修复成功。
	OutcomeRepaired
	// OutcomePartial 部分（含全部）需修复副本写入失败。
	OutcomePartial
	// OutcomeNoSource 没有任何自洽副本，无可用来源。
	OutcomeNoSource
	// OutcomeCommittedLost 最大自洽版本低于已提交版本。
	OutcomeCommittedLost
	// OutcomeConflict 最大自洽版本下存在不同保存摘要。
	OutcomeConflict
)

// RepairKind 标记一个副本在修复计划中的位置。
type RepairKind int

const (
	RepairBitrot RepairKind = iota // 位腐副本
	RepairStale                    // 自洽但版本低于权威
)

func (o Outcome) String() string {
	switch o {
	case OutcomeNoRepair:
		return "no-repair-needed"
	case OutcomeRepaired:
		return "repaired"
	case OutcomePartial:
		return "partial-repair"
	case OutcomeNoSource:
		return "no-available-source"
	case OutcomeCommittedLost:
		return "committed-data-lost"
	case OutcomeConflict:
		return "version-conflict"
	default:
		return "unknown"
	}
}

// Replica 是块在一个节点上的副本。版本号元数据可信。
type Replica struct {
	Node         int
	Version      int64
	SavedDigest  string // 写入时保存的内容摘要
	ActualDigest string // 巡检时实际读出计算得到的摘要
}

// Intact 报告副本是否自洽（保存摘要等于实际摘要）。
func (r Replica) Intact() bool { return r.SavedDigest == r.ActualDigest }

// RepairTarget 描述一个待修复副本及其被修复原因。
type RepairTarget struct {
	Node int
	Kind RepairKind
}

// Arbitration 是一次仲裁判定的纯结果，不包含任何状态修改。
type Arbitration struct {
	Outcome          Outcome
	Quorum           int
	CommittedVersion int64 // 全部副本版本降序第 quorum 个；无副本时为 0
	AuthorityVersion int64
	AuthorityDigest  string
	Targets          []RepairTarget
}

// PatrolResult 是一次巡检对外可见的完整结果。
type PatrolResult struct {
	Arbitration
	Time        Time
	Repaired    []int // 修复写成功的节点
	FailedNodes []int // 修复写失败的节点
}

// Alert 追加式告警条目，按发生次序排列、不合并。
type Alert struct {
	Seq     int
	Time    Time
	BlockID int
	Outcome Outcome
	Detail  string
}

// dueEntry 是到期索引中的一条记录：(下次到期时刻, 块号)。
type dueEntry struct {
	dueAt Time
	block int
}
