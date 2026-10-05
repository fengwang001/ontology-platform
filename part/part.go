// Package part 维护分区状态（Missing 或 Committed(ver>=1)）与完整水位 W。
//
// W 是最大的 w 使 0..w 全部 Committed（不存在时为 -1），只增不减。
// 版本只增不减；Missing 分区的版本视为 0。
// Store 不加锁，并发串行化由上层（mark 门面）负责。
package part

import "errors"

// ErrAlready 表示对已 Committed 的分区再次实时提交。
var ErrAlready = errors.New("part: partition already committed")

// Store 是分区状态存储。vers 仅保存已提交分区，缺失键即 Missing。
type Store struct {
	vers   map[int]int
	w      int
	probes int64 // 推进 W 时探测的分区数（累计）
}

// NewStore 返回空存储，W 初始为 -1。
func NewStore() *Store {
	return &Store{vers: make(map[int]int), w: -1}
}

// Ver 返回分区版本，Missing 为 0。
func (s *Store) Ver(p int) int { return s.vers[p] }

// Committed 报告分区是否已提交。
func (s *Store) Committed(p int) bool { return s.vers[p] >= 1 }

// W 返回完整水位。
func (s *Store) W() int { return s.w }

// Probes 返回推进 W 累计探测的分区数。
func (s *Store) Probes() int64 { return s.probes }

// Commit 实时提交：Missing 变为 Committed 且 ver=1；已提交报 ErrAlready。
func (s *Store) Commit(p int) error {
	if s.vers[p] >= 1 {
		return ErrAlready
	}
	s.vers[p] = 1
	s.advance()
	return nil
}

// Bump 把闭区间 [a,b] 内每个分区版本加 1（Missing 视为 0，提交后为 1），
// 按升序返回提交前各分区版本（下标 i 对应分区 a+i）。
func (s *Store) Bump(a, b int) []int {
	pre := make([]int, 0, b-a+1)
	for p := a; p <= b; p++ {
		pre = append(pre, s.vers[p])
		s.vers[p]++
	}
	s.advance()
	return pre
}

// advance 从 W+1 起逐个探测，推进到第一个 Missing 前为止。
// 每次调用 probes 增量恰为 (新W − 旧W) + 1。
func (s *Store) advance() {
	for {
		s.probes++
		if s.vers[s.w+1] < 1 {
			return
		}
		s.w++
	}
}
