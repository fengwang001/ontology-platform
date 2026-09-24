// Package eff 模拟外部副作用存储：单序号读写，覆盖语义（幂等）。
package eff

// Store 是进程内副作用存储，store[seq] = eff。
// 非并发安全；并发控制由上层 txn 负责。
type Store struct {
	m map[int64]int64
}

// New 返回空存储。
func New() *Store { return &Store{m: make(map[int64]int64)} }

// Put 幂等覆盖写入 store[seq] = eff；重复写入同值不改变状态。
func (s *Store) Put(seq, eff int64) { s.m[seq] = eff }

// Get 读取 store[seq]，ok 表示该序号的效果是否已写入。
func (s *Store) Get(seq int64) (eff int64, ok bool) {
	eff, ok = s.m[seq]
	return eff, ok
}

// Snapshot 返回全部已写副作用的副本（用于重启后核对与测试对照）。
func (s *Store) Snapshot() map[int64]int64 {
	out := make(map[int64]int64, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}
