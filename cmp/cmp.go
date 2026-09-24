// Package cmp 在 lsn.Set 之上提供重编号双向映射：
// 旧 LSN 按升序位次得新号（0 起），新号按下标取旧 LSN，二者互逆。
// 对外屏蔽 lsn，使上层只依赖本包。
package cmp

import "ontology/lsn"

// 四类可判定哨兵错误，互不相同（定义在 lsn，此处再导出）。
var (
	ErrNegative   = lsn.ErrNegative
	ErrDuplicate  = lsn.ErrDuplicate
	ErrUnknown    = lsn.ErrUnknown
	ErrOutOfRange = lsn.ErrOutOfRange
)

// Map 是旧号↔新号的双向映射，内部持有有序唯一集合。
type Map struct {
	set *lsn.Set
}

// New 返回空映射。
func New() *Map { return &Map{set: lsn.New()} }

// Add 插入一条 LSN；负号或重复整体失败，状态不变。
func (m *Map) Add(v int64) error { return m.set.Add(v) }

// Len 返回记录数 n。
func (m *Map) Len() int { return m.set.Len() }

// Holes 返回 [min,max] 内缺失整数，升序。
func (m *Map) Holes() []int64 { return m.set.Holes() }

// FindNew 返回旧 LSN 对应的新号；未知 LSN 报 ErrUnknown。
func (m *Map) FindNew(old int64) (int64, error) {
	i, err := m.set.IndexOf(old)
	if err != nil {
		return 0, err
	}
	return int64(i), nil
}

// FindOld 返回新号对应的旧 LSN；越界报 ErrOutOfRange。
func (m *Map) FindOld(new int64) (int64, error) {
	return m.set.ValueAt(int(new))
}
