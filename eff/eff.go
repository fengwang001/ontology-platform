// Package eff 是单序号副作用存储：按 seq 读写，Put 为幂等覆盖语义。
// 它不依赖其他包。这里的 map 模拟"已持久化"的外部存储。
package eff

// Store 保存 seq -> effect。
type Store struct {
	m map[int64]int64
}

// New 创建空存储。
func New() *Store {
	return &Store{m: make(map[int64]int64)}
}

// Put 写入（覆盖）store[seq] = eff。重复 Put 以最后一次为准。
func (s *Store) Put(seq, eff int64) {
	s.m[seq] = eff
}

// Get 读取 store[seq]；ok 表示该序号是否已写。
func (s *Store) Get(seq int64) (val int64, ok bool) {
	val, ok = s.m[seq]
	return val, ok
}

// Above 返回全部已写且 seq > c 的序号（无序），供上层求在途集合。
func (s *Store) Above(c int64) []int64 {
	out := make([]int64, 0)
	for k := range s.m {
		if k > c {
			out = append(out, k)
		}
	}
	return out
}

// Clone 复制一份内容相同的新 Store，模拟崩溃后从持久介质重新加载。
func (s *Store) Clone() *Store {
	c := New()
	for k, v := range s.m {
		c.m[k] = v
	}
	return c
}

// Snapshot 返回当前全部 seq->eff 的副本，供逐项核对。
func (s *Store) Snapshot() map[int64]int64 {
	m := make(map[int64]int64, len(s.m))
	for k, v := range s.m {
		m[k] = v
	}
	return m
}
