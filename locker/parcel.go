package locker

// parcel 是柜内一件快件的运行时状态。
type parcel struct {
	tracking    TrackingNo
	size        Size
	phoneTail   string
	cell        CellID
	code        Code
	depositedAt int64
	paid        int64 // 已缴金额（货币单位，非负）
	mismatches  int   // 连续手机号不符次数
	locked      bool
}

// due 返回 now 时刻尚欠金额。
func (p *parcel) due(cfg Config, now int64) int64 {
	fee := storageFee(cfg, p.depositedAt, now)
	if fee > p.paid {
		return fee - p.paid
	}
	return 0
}

// timedOut 是超时判定：自存入时刻起恰好满上限即超时（时刻纯函数）。
func (p *parcel) timedOut(cfg Config, now int64) bool {
	return now-p.depositedAt >= cfg.MaxStorage
}
