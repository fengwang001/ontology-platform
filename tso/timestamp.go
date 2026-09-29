package tso

import "strconv"

// Timestamp 是由物理毫秒与逻辑计数组成的混合逻辑时钟时间戳，
// 按 (Physical, Logical) 字典序比较。
type Timestamp struct {
	Physical int64
	Logical  int64
}

// Less 报告 t 是否严格小于 other（字典序）。
func (t Timestamp) Less(other Timestamp) bool {
	if t.Physical != other.Physical {
		return t.Physical < other.Physical
	}
	return t.Logical < other.Logical
}

// String 返回 "物理毫秒.逻辑计数" 形式的文本。
func (t Timestamp) String() string {
	return strconv.FormatInt(t.Physical, 10) + "." + strconv.FormatInt(t.Logical, 10)
}
