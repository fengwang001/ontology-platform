package lineage

// shard 是分片的内部可变状态。
type shard struct {
	id        ShardID
	lo, hi    int
	closed    bool
	appended  int64 // 已追加条数；关闭后即结束位置
	committed int64 // 已提交进度
	parents   []ShardID
	holder    WorkerID // 最近一次被授予的工作者（可能已过期）
	expires   Clock
}

// drained 判定分片是否已排空：已关闭且提交进度等于结束位置。
// 关闭时无记录（appended==0）立即满足。
func (s *shard) drained() bool {
	return s.closed && s.committed == s.appended
}

// leaseValid 判断租约在时钟 now 下是否仍然有效。
// 规则：now >= expires 即失效，因此严格小于才有效。
func (s *shard) leaseValid(now Clock) bool {
	return s.holder != "" && now < s.expires
}
