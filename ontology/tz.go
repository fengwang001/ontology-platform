package ontology

import "time"

// Zone 表示一个固定偏移的时区。生产环境可替换为 IANA 时区数据库实现，
// 本实现使用固定偏移以保证测试结果与宿主机器的 tzdata 无关。
type Zone struct {
	ID            string
	OffsetMinutes int // 相对 UTC 向东的偏移分钟数
}

// ToUTC 把该时区下的本地墙钟时间解释为 UTC 时刻。
func (z Zone) ToUTC(local time.Time) time.Time {
	return local.Add(-time.Duration(z.OffsetMinutes) * time.Minute)
}

var zoneRegistry = map[string]Zone{
	"UTC":       {ID: "UTC", OffsetMinutes: 0},
	"UTC+08:00": {ID: "UTC+08:00", OffsetMinutes: 8 * 60},
	"UTC+05:30": {ID: "UTC+05:30", OffsetMinutes: 5*60 + 30},
	"UTC-05:00": {ID: "UTC-05:00", OffsetMinutes: -5 * 60},
	"UTC-08:00": {ID: "UTC-08:00", OffsetMinutes: -8 * 60},
	"UTC+00:30": {ID: "UTC+00:30", OffsetMinutes: 30},
}

// LookupZone 按 ID 查找时区。
func LookupZone(id string) (Zone, bool) {
	z, ok := zoneRegistry[id]
	return z, ok
}
