package orphanreclaim

// RetentionBasis 描述判定结论所依据的保留证据；二者互斥且按层次出现。
type RetentionBasis struct {
	Layer     string   // "independent" | "joint" | "none"
	LinkTypes []string // independent：命中的单个类型；joint：完整组成员；none：空
}

// DecisionRecord 记录一次孤儿判定的输入、输出与据以判定的入边组合。
// 入边组合以「类型 -> 该类型入边条数」呈现：判定只读取类型桶，
// 因此桶数量（len(InCounts) 的上界由配置类型数决定）即核对复杂度凭据。
type DecisionRecord struct {
	Seq            uint64         `json:"seq"`
	At             int64          `json:"at"`
	Object         string         `json:"object"`
	Trigger        string         `json:"trigger"` // add_link | remove_link | query | advance_recheck
	LinkType       string         `json:"link_type,omitempty"`
	Source         string         `json:"source,omitempty"`
	InCounts       map[string]int `json:"in_counts"`
	Basis          RetentionBasis `json:"basis"`
	Orphan         bool           `json:"orphan"`
	PreviousGen    Generation     `json:"previous_gen"`
	Outcome        string         `json:"outcome"` // queued_gen1 | stayed_gen1|gen2 | rescued | already_non_orphan
	BucketsChecked int            `json:"buckets_checked"`
}

// AdvanceRecord 记录一次代际推进扫描的输入与输出。
type AdvanceRecord struct {
	Seq       uint64            `json:"seq"`
	At        int64             `json:"at"`
	Purged    []string          `json:"purged"`
	Promoted  []string          `json:"promoted"`
	Rechecked map[string]string `json:"rechecked"` // 对象 -> 到期重评后被救回("rescued")的记录
}

// Logger 接收判定与推进日志。
type Logger interface {
	LogDecision(DecisionRecord)
	LogAdvance(AdvanceRecord)
}

// nopLogger 丢弃全部日志。
type nopLogger struct{}

func (nopLogger) LogDecision(DecisionRecord) {}
func (nopLogger) LogAdvance(AdvanceRecord)   {}
