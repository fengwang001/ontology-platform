package lessor

// Lease 租约的内部表示。
type Lease struct {
	id     int64               // 调用方给定的正整数编号
	g      int64               // 有效 TTL：g = max(ttl, MinTTL)
	sv     int64               // 检查点剩余寿命，初值 0
	x      int64               // 到期时刻（仅主有意义）
	keys   map[string]struct{} // 挂靠键集合
	hindex int                 // 在到期最小堆中的下标
}

// Revoked 一次撤销的结果：租约编号与被摘下的键（升序）。
type Revoked struct {
	ID   int64
	Keys []string
}
