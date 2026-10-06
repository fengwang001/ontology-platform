package remittance

// quote 为一次锁汇报价。
type quote struct {
	id        string
	remitter  string
	srcAmount int64
	rate      int64 // 每源币种最小单位折合目标币种最小单位的百万分之几
	createdAt int64
	consumed  bool
}

// expired 判定报价在 now 是否已过期：到期时刻恰等仍有效，晚一秒即过期。
// 即 now <= createdAt+ttl 有效，now > createdAt+ttl 过期（溢出按永不过期处理）。
func (q *quote) expired(now, ttl int64) bool {
	expiry := q.createdAt + ttl
	if expiry < q.createdAt { // int64 溢出，视为永不过期
		return false
	}
	return now > expiry
}
