package booking

// Config 为系统构造参数，单位均为秒。
type Config struct {
	// DispatchLead 派单提前量：start-now <= DispatchLead 时（恰等）释放。
	DispatchLead int64
	// RescheduleLead 改期截止提前量：now >= start-RescheduleLead 时（恰等）禁止改期。
	RescheduleLead int64
	// MinBookAhead 最早可预约提前量（最小提前量，两端取等允许）。
	MinBookAhead int64
	// MaxBookAhead 最晚可预约提前量（最大提前量，两端取等允许）。
	MaxBookAhead int64
	// MaxPostponeSpan 顺延候选与原时段起点之差的最大跨度（恰等允许）。
	MaxPostponeSpan int64
}
