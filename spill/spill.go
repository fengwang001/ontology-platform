// Package spill 决定值内联还是溢出（阈值边界），并持有引用与溢出块的状态。
package spill

// IsInline 判定长度 n 的值在阈值 t 下是否内联：n <= t 内联，否则溢出。
func IsInline(n, t int) bool { return n <= t }

// Store 保存主记录（内联值 Inline、溢出引用 Ref）与溢出表 Blocks。
type Store struct {
	T      int
	Inline map[string][]byte
	Ref    map[string]uint64
	Blocks map[uint64][]byte
	next   uint64
}

// NewStore 创建空状态，块号从 1 起单调递增、永不复用。
func NewStore(t int) *Store {
	return &Store{
		T:      t,
		Inline: map[string][]byte{},
		Ref:    map[string]uint64{},
		Blocks: map[uint64][]byte{},
		next:   1,
	}
}

// Alloc 分配下一个溢出块号。
func (s *Store) Alloc() uint64 {
	b := s.next
	s.next++
	return b
}
