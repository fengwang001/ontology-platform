// Package entry 定义单 Key 的值、最近写入时间 Ts 与过期判定。
// 不依赖其他包。
package entry

// Entry 是单 Key 的状态：值与最近一次写入时间 Ts。
type Entry struct {
	Value string
	Ts    int64
}

// Expired 判定过期：now − Ts ≥ TTL（含等于）。
// 这是全工程唯一的过期判定出口，任何其他判定都是错的。
func Expired(now, ts, ttl int64) bool {
	return now-ts >= ttl
}
