// Package ttl 提供墙钟 TTL 过期判定。清理时钟（now）与事件时钟（last）在此交汇，
// 但本包只做纯判定，不依赖任何其他包。
package ttl

// Expired 报告条目在墙钟时刻 now 是否已过期：now-last >= ttl。
// 边界：恰好等于 ttl 算已过期。
func Expired(now, last, ttl int64) bool {
	return now-last >= ttl
}
