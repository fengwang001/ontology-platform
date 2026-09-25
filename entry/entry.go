// Package entry 定义单 Key 的值、写入时间戳与过期判定。不依赖其他包。
package entry

// Entry 是单个 Key 的状态：值与最近一次写入时间 Ts。
type Entry struct {
	Value string
	Ts    int64
}

// Expired 判定时间戳为 ts 的条目在逻辑时刻 now、生存期 ttl 下是否过期。
// 过期当且仅当 now−ts ≥ ttl（含等于）。
func Expired(ts, now, ttl int64) bool {
	return now-ts >= ttl
}
