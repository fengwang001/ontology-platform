package locker

// storageFee 按时刻纯函数计算滞留费：
// 恰好等于免费时长视为未超出；超出部分每满一个完整计费周期收一次单价；
// 不足一个周期不收；单件费用受封顶限制。
func storageFee(cfg Config, depositedAt, now int64) int64 {
	elapsed := now - depositedAt
	if elapsed <= cfg.FreeStorage {
		// elapsed == FreeStorage 时视为未超出。
		return 0
	}
	periods := (elapsed - cfg.FreeStorage) / cfg.BillingPeriod
	fee := periods * cfg.FeePerPeriod
	if cfg.FeeCap > 0 && fee > cfg.FeeCap {
		fee = cfg.FeeCap
	}
	return fee
}
