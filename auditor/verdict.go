package auditor

// Category 是裁决类别。
type Category int

const (
	// Pending 表示桶尚未结算，裁决待定。
	Pending Category = iota
	// Balanced 表示守恒、逐跳平衡且无缺报。
	Balanced
	// Loss 表示某跳下一阶段收入小于上一阶段发出。
	Loss
	// Duplicate 表示某跳下一阶段收入大于上一阶段发出。
	Duplicate
	// NonConservation 表示某阶段 收入×f 不等于 发出+丢弃。
	NonConservation
	// MissingReport 表示无违规但存在从未上报的阶段。
	MissingReport
)

func (c Category) String() string {
	switch c {
	case Pending:
		return "待定"
	case Balanced:
		return "平衡"
	case Loss:
		return "丢失"
	case Duplicate:
		return "重复"
	case NonConservation:
		return "不守恒"
	case MissingReport:
		return "缺报"
	default:
		return "未知"
	}
}

// Verdict 是一个桶的裁决结果。
type Verdict struct {
	Category Category
	// Stage 对 NonConservation / MissingReport 有效，为违规或缺报的阶段编号。
	Stage int
	// Hop 对 Loss / Duplicate 有效，表示从 Hop 到 Hop+1 的跳。
	Hop int
	// Delta 对 Loss / Duplicate 有效，为两端计数差额的绝对值。
	Delta int64
	// Revision 为修订号；结算时首次裁决为 0，之后裁决变化每次加一。
	Revision int
	// Settled 表示桶是否已结算。
	Settled bool
	// Reason 记录判定依据，便于日志与审计。
	Reason string
}

func (v Verdict) equal(o Verdict) bool {
	return v.Category == o.Category && v.Stage == o.Stage &&
		v.Hop == o.Hop && v.Delta == o.Delta
}
