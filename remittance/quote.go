package remittance

// quote 是一条锁汇报价。
type quote struct {
	id        int64
	sender    string
	sourceCCY string
	targetCCY string
	amount    int64
	ratePPM   int64
	createdAt int64
	expiresAt int64 // createdAt + T；恰等时刻仍有效
	consumed  bool  // 被一次“被接受”的提交消耗
}

// validAt 报价在 now 时刻是否仍可用（未消耗且未过期）。
// 到期时刻恰等仍有效；晚于 expiresAt 一秒即过期。
func (q *quote) validAt(now int64) bool { return !q.consumed && now <= q.expiresAt }
