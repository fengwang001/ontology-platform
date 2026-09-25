// Package ver 定义版本链中的单个版本：一个提交时间戳，配一个值或删除标记。
package ver

// Version 是 MVCC 版本链上的一个不可变版本。
// 一旦构造，字段永不修改（链不可变性的基础）。
type Version struct {
	TS    int64  // commit_time，同一条链内互不相同
	Value string // 值；tombstone 时无意义
	Del   bool   // true 表示该版本是删除标记（tombstone）
}

// Value 构造一个携带值的版本。
func ValueVersion(ts int64, value string) Version {
	return Version{TS: ts, Value: value}
}

// Tombstone 构造一个删除标记版本。
func Tombstone(ts int64) Version {
	return Version{TS: ts, Del: true}
}

// Less 报告 v 是否应排在 other 之前（按 ts 升序）。
func (v Version) Less(other Version) bool {
	return v.TS < other.TS
}
