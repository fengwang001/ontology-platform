// Package spill 模拟溢写存储：按块号存取行块，容量有限，块号全局递增且不复用。
package spill

import "sort"

// Store 是内存模拟的溢写存储。不是并发安全的，调用方（rbuf）负责串行化。
type Store struct {
	max    int
	next   int              // 下一个块号，只增不减，删除不复用
	blocks map[int][]string // 块号 -> 行
	owner  map[int]int      // 块号 -> 所属事务号
}

// New 创建容量为 maxSpill 个块的溢写存储。
func New(maxSpill int) *Store {
	return &Store{max: maxSpill, blocks: map[int][]string{}, owner: map[int]int{}}
}

// Full 报告存储是否已满（再写一个块前必须检查）。
func (s *Store) Full() bool { return len(s.blocks) >= s.max }

// Count 返回当前存放的块数。
func (s *Store) Count() int { return len(s.blocks) }

// Put 把 rows 按原顺序打包成一个块写入，返回全局递增的块号。
// 调用前必须保证 !Full()。rows 会被拷贝，调用方之后可安全复用底层数组。
func (s *Store) Put(tx int, rows []string) int {
	cp := make([]string, len(rows))
	copy(cp, rows)
	no := s.next
	s.next++
	s.blocks[no] = cp
	s.owner[no] = tx
	return no
}

// Get 按块号读回行（保持写入顺序）。块不存在时返回 nil。
func (s *Store) Get(no int) []string { return s.blocks[no] }

// DeleteTx 删除某事务的全部块，返回删除的块数。空出的只是容量，块号不复用。
func (s *Store) DeleteTx(tx int) int {
	n := 0
	for no, o := range s.owner {
		if o == tx {
			delete(s.blocks, no)
			delete(s.owner, no)
			n++
		}
	}
	return n
}

// Blocks 返回当前存储中的块号，升序。
func (s *Store) Blocks() []int {
	out := make([]int, 0, len(s.blocks))
	for no := range s.blocks {
		out = append(out, no)
	}
	sort.Ints(out)
	return out
}
