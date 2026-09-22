package version

// Version 是单调递增的版本号。零值表示"无版本"。
type Version uint64

// None 是零值，表示从未收到过任何版本。
const None Version = 0

// New 根据原始序号构造版本号；0 被规范化为 None。
func New(n uint64) Version { return Version(n) }

// IsZero 报告该版本是否为"无版本"。
func (v Version) IsZero() bool { return v == None }

// Before 报告 v 是否严格早于 other。None 早于一切非零版本。
func (v Version) Before(other Version) bool { return v < other }

// After 报告 v 是否严格晚于 other。
func (v Version) After(other Version) bool { return v > other }

// Newer 报告 v 是否严格新于 other。
func (v Version) Newer(other Version) bool { return v > other }

// Max 返回两个版本中较大的一个。
func (v Version) Max(other Version) Version {
	if other > v {
		return other
	}
	return v
}
