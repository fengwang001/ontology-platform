package surge

// TierEvent 为一次导致档位变化的评估记录。
type TierEvent struct {
	Area     string
	At       int64
	FromTier int
	ToTier   int
	Ratio    float64
	RatioInf bool // 比率为正/0 的无穷大
}

// SubsidyEntry 为一笔骑手补贴账目。
type SubsidyEntry struct {
	RiderID string
	OrderID string
	At      int64 // 完成时刻
	Tier    int   // 订单锁定档
	Amount  int64 // 实际计入额（豁免时为 0）
	Waived  bool  // 是否为迟到补贴豁免
}
