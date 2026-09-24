// Package page 在可并发增删的有序数据集上提供 keyset 游标翻页。
//
// 每页通过二分定位复合键边界（O(log N) 次比较）再顺序取 n 行，
// 绝不从头全扫。翻页是纯函数：不保存任何推进状态，同一游标并发
// 翻页得到逐行相同的页。
package page

import (
	"slices"
	"sync"
	"sync/atomic"

	"ontology/cursor"
	"ontology/row"
)

// comparisons 是非导出计数器，记录本次翻页比较过的行数。
var comparisons atomic.Int64

// LastComparisons 返回最近一次翻页的比较次数（供效率审计）。
func LastComparisons() int64 {
	return comparisons.Load()
}

// Store 是进程内存中的有序数据集，按复合键 (Key, ID) 升序。
// 增删与翻页可并发进行，由 RWMutex 保护。
type Store struct {
	mu   sync.RWMutex
	rows []row.Row
}

// NewStore 返回空数据集。
func NewStore() *Store {
	return &Store{}
}

// Upsert 插入一行；ID 已存在时替换其排序键。
func (s *Store) Upsert(r row.Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = slices.DeleteFunc(s.rows, func(x row.Row) bool { return x.ID == r.ID })
	idx, _ := slices.BinarySearchFunc(s.rows, r, row.Compare)
	s.rows = slices.Insert(s.rows, idx, r)
}

// Delete 按 ID 删除一行，返回该行是否存在过。
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.rows)
	s.rows = slices.DeleteFunc(s.rows, func(x row.Row) bool { return x.ID == id })
	return len(s.rows) != n
}

// Len 返回当前行数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// Snapshot 返回当前全部行的有序副本。
func (s *Store) Snapshot() []row.Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.rows)
}

// Result 是一页的结果。Next 锚在页尾行（方向=正向），Prev 锚在
// 页首行（方向=反向）；空页的两个游标都是零游标。
type Result struct {
	Rows []row.Row
	Next cursor.Cursor
	Prev cursor.Cursor
}

func makeResult(rows []row.Row) Result {
	if len(rows) == 0 {
		return Result{Rows: rows}
	}
	first, last := rows[0], rows[len(rows)-1]
	return Result{
		Rows: rows,
		Next: cursor.Cursor{Dir: cursor.Forward, Key: last.Key, ID: last.ID},
		Prev: cursor.Cursor{Dir: cursor.Backward, Key: first.Key, ID: first.ID},
	}
}

// Forward 取复合键严格大于 c 的前 n 行。c 为零游标时从头开始。
// c 是反向游标时报 cursor.ErrDirectionMismatch。
func Forward(s *Store, c cursor.Cursor, n int) (Result, error) {
	if c.Dir == cursor.Backward {
		return Result{}, cursor.ErrDirectionMismatch
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	comparisons.Store(0)
	idx := 0
	if !c.IsZero() {
		anchor := row.Row{Key: c.Key, ID: c.ID}
		idx, _ = slices.BinarySearchFunc(s.rows, anchor, func(x, target row.Row) int {
			comparisons.Add(1)
			cmp := row.Compare(x, target)
			if cmp == 0 {
				return 0 // 命中锚点本身，插入点在其后，即严格大于
			}
			return cmp
		})
		if idx < len(s.rows) && row.Compare(s.rows[idx], anchor) == 0 {
			idx++
		}
	}
	end := min(idx+n, len(s.rows))
	return makeResult(slices.Clone(s.rows[idx:max(idx, end)])), nil
}

// Backward 取复合键严格小于 c 的最大的 n 行，页内仍按升序排列。
// c 为零游标时从末尾开始（取最后一页）。c 是正向游标时报
// cursor.ErrDirectionMismatch。
func Backward(s *Store, c cursor.Cursor, n int) (Result, error) {
	if c.Dir == cursor.Forward {
		return Result{}, cursor.ErrDirectionMismatch
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	comparisons.Store(0)
	idx := len(s.rows)
	if !c.IsZero() {
		anchor := row.Row{Key: c.Key, ID: c.ID}
		idx, _ = slices.BinarySearchFunc(s.rows, anchor, func(x, target row.Row) int {
			comparisons.Add(1)
			return row.Compare(x, target)
		})
	}
	start := max(idx-n, 0)
	return makeResult(slices.Clone(s.rows[start:idx])), nil
}
