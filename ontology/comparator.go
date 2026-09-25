package ontology

// CountingComparator 统计排序值两两比较的次数。

// 比较语义与排名方向无关：它只负责“值的有序性”，
// Compare(a,b) < 0 表示 a 在升序意义下排在 b 前面。
// +0.0 与 -0.0 视为相等；NaN 不会进入比较（调用前已拒绝）。
type CountingComparator struct {
	count int64
}

// Count 返回迄今为止发生的比较次数。
func (c *CountingComparator) Count() int64 {
	if c == nil {
		return 0
	}
	return c.count
}

// reset 将计数清零，供同一次 Rank 调用内部初始化使用。
func (c *CountingComparator) reset() {
	if c != nil {
		c.count = 0
	}
}

// Compare 返回 -1/0/1 表示 a 相对 b 升序在前/相等/在后，
// 并把本次比较计入计数器。
func (c *CountingComparator) Compare(a, b float64) int {
	if c != nil {
		c.count++
	}
	return compareValues(a, b)
}

// compareValues 是不带计数的值比较：-1/0/1。

// 关键点：math.Signbit 让 +0.0 与 -0.0 判为相等；
	// ±Inf 自然落到两端（+Inf 最大、-Inf 最小）。
func compareValues(a, b float64) int {
	if a == b {
		// a==b 为 true 时二者数值相等，含同为 +Inf/-Inf、
		// 以及 +0.0 与 -0.0 的情形。
		return 0
	}
	if a < b {
		return -1
	}
	return 1
}
