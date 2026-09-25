// Package col 是列式物化视图最底层：槽对齐三列、存活墓碑位、rowID→槽映射与压缩；不依赖其他包。
package col

import (
	"errors"
	"sync/atomic"
)

// Col 标识固定三列之一。
type Col int

const (
	A Col = iota
	B
	C
)

// 底层哨兵错误；view 层负责映射成对外可判定错误。
var (
	ErrNoSuchRow = errors.New("col: row id was never allocated")
	ErrTombstone = errors.New("col: row has been deleted")
)

// Store 中 a/b/c 与 alive 按下标对齐到槽，slotOf 按下标对齐到 rowID（已删
// 为 -1）。getChecks 非导出原子计数：成功 Get 检查的槽位数，直接下标恒为 1。
type Store struct {
	a, b, c   []int64
	alive     []bool
	slotOf    []int
	getChecks atomic.Int64
}

// New 创建空存储。
func New() *Store { return &Store{} }

// AppendRow 在末尾槽登记新行；id 必须是顺序的下一个 rowID（绝不复用墓碑槽）。
func (s *Store) AppendRow(id int, x, y, z int64) {
	if id != len(s.slotOf) {
		panic("col: non-sequential row id") // 仅由 view 顺序分配调用，属内部不变量
	}
	s.a, s.b, s.c = append(s.a, x), append(s.b, y), append(s.c, z)
	s.alive = append(s.alive, true)
	s.slotOf = append(s.slotOf, len(s.a)-1)
}

// locate 经 slotOf 直接下标定位：只触及至多一个槽，不做整表扫描。
func (s *Store) locate(id int) (int, error) {
	if id < 0 || id >= len(s.slotOf) {
		return 0, ErrNoSuchRow
	}
	if slot := s.slotOf[id]; slot >= 0 {
		return slot, nil
	}
	return 0, ErrTombstone
}

// Update 修改某行某列；定位失败即整体返回，不触碰任何列值。
func (s *Store) Update(id int, k Col, v int64) error {
	slot, err := s.locate(id)
	if err != nil {
		return err
	}
	switch k {
	case A:
		s.a[slot] = v
	case B:
		s.b[slot] = v
	case C:
		s.c[slot] = v
	}
	return nil
}

// Delete 置墓碑：标记死槽、解绑 rowID，列中旧值原样保留。
func (s *Store) Delete(id int) error {
	slot, err := s.locate(id)
	if err != nil {
		return err
	}
	s.alive[slot] = false
	s.slotOf[id] = -1
	return nil
}

// Compact 把存活槽按原序重排并重建 slotOf；死 rowID 仍 -1，存活 rowID 与
// 三列值绑定不变。新行恒 append 末尾，按 slotOf（rowID 升序）遍历即原序。
func (s *Store) Compact() {
	na := make([]int64, 0, len(s.a))
	nb, nc := make([]int64, 0, len(s.b)), make([]int64, 0, len(s.c))
	nl := make([]bool, 0, len(s.alive))
	ns := make([]int, len(s.slotOf))
	for i := range ns {
		ns[i] = -1
	}
	for id, slot := range s.slotOf {
		if slot < 0 {
			continue
		}
		ns[id] = len(na)
		na, nb, nc = append(na, s.a[slot]), append(nb, s.b[slot]), append(nc, s.c[slot])
		nl = append(nl, true)
	}
	s.a, s.b, s.c, s.alive, s.slotOf = na, nb, nc, nl, ns
}

// Get 返回某行三列值；成功路径只检查目标槽这一个槽。
func (s *Store) Get(id int) (int64, int64, int64, error) {
	slot, err := s.locate(id)
	if err != nil {
		return 0, 0, 0, err
	}
	s.getChecks.Store(1)
	return s.a[slot], s.b[slot], s.c[slot], nil
}

// AliveSlots 返回所有存活槽号（槽顺序恒等于存活 rowID 升序）。
func (s *Store) AliveSlots() []int {
	out := make([]int, 0, len(s.alive))
	for slot, live := range s.alive {
		if live {
			out = append(out, slot)
		}
	}
	return out
}

// At 取指定槽、指定列的值。
func (s *Store) At(slot int, k Col) int64 {
	return [3][]int64{s.a, s.b, s.c}[k][slot]
}

// VerifyLookupO1 多档规模下核验 Get 检查槽数恒为小常数 1；只回布尔判定，
// 绝不经导出接口暴露计数器数值。
func VerifyLookupO1(sizes []int) bool {
	for _, m := range sizes {
		t := New()
		for j := 0; j < m; j++ {
			t.AppendRow(j, int64(j), 0, 0)
		}
		if _, _, _, err := t.Get(m / 2); err != nil || t.getChecks.Load() != 1 {
			return false
		}
	}
	return len(sizes) > 0
}

// Snapshot 返回内部状态拷贝，仅供演示/诊断逐格核对。
func (s *Store) Snapshot() (a, b, c []int64, alive []bool, slotOf []int) {
	return append([]int64(nil), s.a...), append([]int64(nil), s.b...), append([]int64(nil), s.c...), append([]bool(nil), s.alive...), append([]int(nil), s.slotOf...)
}
