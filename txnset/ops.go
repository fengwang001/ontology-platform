package txnset

// Merge 返回 a 与 b 的并集（同一来源区间合并重叠与相邻段）。
// 不修改 a 或 b，可并发调用。
func Merge(a, b *Set) *Set {
	return nil
}

// Subtract 返回 a 中不属于 b 的部分（a \ b），用于断点续传中
// 从源端事务集合扣除本地已执行集合。不修改 a 或 b，可并发调用。
func Subtract(a, b *Set) *Set {
	return nil
}

// IsEmpty 报告集合是否为空。
func (s *Set) IsEmpty() bool {
	return true
}

// Equal 报告两个集合的规范化内容是否完全一致。
func (s *Set) Equal(o *Set) bool {
	return false
}
