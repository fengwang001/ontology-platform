package settlement

// report.go —— 日终批处理的判定明细，用于可复现日志。

// OrderDecision 记录单个指令在一次批处理中的判定依据。
type OrderDecision struct {
	ID              int64
	RemainingBefore int64 // 批处理开始时未交割量
	SellerAvail     int64 // 扣除本批更早指令占用后卖方可用券
	BuyerMaxQty     int64 // 扣除占用后买方现金能买到的最大数量
	Delivered       int64 // 本批实际交割量
	RemainingAfter  int64
	Failed          bool
	Responsible     string // "seller" | "buyer" | ""
	Penalty         int64
	ForceClosed     bool
	Compensation    int64
}

// BatchReport 是一次日终批处理的完整判定明细。
type BatchReport struct {
	Day       int64
	Decisions []OrderDecision
}

// LastReport 返回最近一次批处理的判定明细；尚未批处理时返回错误。
func (s *System) LastReport() (BatchReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastBatchDay == 0 {
		return BatchReport{}, ErrOrdering
	}
	return s.lastReport, nil
}
