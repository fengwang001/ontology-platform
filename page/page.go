// Package page 在一个可并发增删的内存有序数据集上做游标（keyset）分页。
package page

import (
	"errors"
	"sort"
	"sync"

	"ontology/cursor"
	"ontology/row"
)

var (
	// ErrExists：插入的行 ID 或复合键与已有行冲突。
	ErrExists = errors.New("page: row already exists")
	// ErrSize：页大小非法（必须 > 0）。
	ErrSize = errors.New("page: invalid page size")
)

// Store 是进程内有序数据集，增删与翻页可并发进行。
type Store struct {
	mu   sync.RWMutex
	rows []row.Row // 始终按复合键 (Score,ID) 升序
}

// NewStore 创建空数据集。
func NewStore() *Store { return &Store{} }

// Len 返回当前行数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// Insert 校验后按复合键位置插入；ID 必须唯一。
func (s *Store) Insert(r row.Row) error {
	if !r.Valid() {
		return row.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := sort.Search(len(s.rows), func(i int) bool { return !row.Less(s.rows[i], r) })
	if pos < len(s.rows) && row.Compare(s.rows[pos], r) == 0 {
		return ErrExists
	}
	for _, x := range s.rows {
		if x.ID == r.ID {
			return ErrExists
		}
	}
	s.rows = append(s.rows, row.Row{})
	copy(s.rows[pos+1:], s.rows[pos:])
	s.rows[pos] = r
	return nil
}

// Delete 按 ID 删除一行；返回是否实际删除。
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := sort.Search(len(s.rows), func(i int) bool { return s.rows[i].ID >= id })
	if pos >= len(s.rows) || s.rows[pos].ID != id {
		// ID 与键序无关，退化线性定位。
		for i, x := range s.rows {
			if x.ID == id {
				pos = i
				goto found
			}
		}
		return false
	}
found:
	s.rows = append(s.rows[:pos], s.rows[pos+1:]...)
	return true
}

// Snapshot 返回当前全部行的有序拷贝。
func (s *Store) Snapshot() []row.Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]row.Row, len(s.rows))
	copy(out, s.rows)
	return out
}

// Result 是一次取页结果。Next/Prev 分别为继续正向/反向翻页的不透明游标（到边界时为空）。
type Result struct {
	Rows []row.Row
	Next []byte
	Prev []byte

	comparisons int // 非导出计数器：本次翻页比较过的行数
}

// Comparisons 返回本次翻页实际比较过的行数（二分 + 顺序扫描）。
func (r Result) Comparisons() int { return r.comparisons }

func pageCursors(first, last row.Row, dir cursor.Direction) (fwd, back []byte) {
	fwd, _ = cursor.Encode(cursor.Cursor{Dir: cursor.Forward, First: first, Last: last})
	back, _ = cursor.Encode(cursor.Cursor{Dir: cursor.Backward, First: first, Last: last})
	if dir == cursor.Forward {
		return fwd, back
	}
	return fwd, back
}

// Forward 正向取一页。raw 为空字节串表示从头开始。
func (s *Store) Forward(raw []byte, size int) (Result, error) {
	if size <= 0 {
		return Result{}, ErrSize
	}
	dec, err := cursor.Decode(raw)
	if err != nil {
		return Result{}, err
	}
	if err := cursor.EnsureDir(dec, cursor.Forward); err != nil {
		return Result{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.rows)
	cmp := 0
	lo, hi := 0, n
	for lo < hi { // 第一个严格大于边界的位置
		mid := int(uint(lo+hi) >> 1)
		cmp++
		if row.After(s.rows[mid], dec.Last) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	end := lo + size
	if end > n {
		end = n
	}
	rows := s.rows[lo:end]
	cmp += len(rows)
	res := Result{Rows: append([]row.Row(nil), rows...), comparisons: cmp}
	if len(res.Rows) == 0 {
		return res, nil
	}
	fwd, back := pageCursors(res.Rows[0], res.Rows[len(res.Rows)-1], cursor.Forward)
	if lo > 0 {
		res.Prev = back
	}
	if end < n {
		res.Next = fwd
	}
	return res, nil
}

// Backward 反向取一页：从边界之前倒取 size 行，再反转为正向页内顺序。
func (s *Store) Backward(raw []byte, size int) (Result, error) {
	if size <= 0 {
		return Result{}, ErrSize
	}
	dec, err := cursor.Decode(raw)
	if err != nil {
		return Result{}, err
	}
	if err := cursor.EnsureDir(dec, cursor.Backward); err != nil {
		return Result{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := len(s.rows)
	cmp := 0
	lo, hi := 0, n
	for lo < hi { // 第一个不严格小于边界的位置
		mid := int(uint(lo+hi) >> 1)
		cmp++
		if row.Before(s.rows[mid], dec.First) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	start := lo - size
	if start < 0 {
		start = 0
	}
	rows := append([]row.Row(nil), s.rows[start:lo]...)
	cmp += len(rows)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	res := Result{Rows: rows, comparisons: cmp}
	if len(res.Rows) == 0 {
		return res, nil
	}
	fwd, back := pageCursors(res.Rows[0], res.Rows[len(res.Rows)-1], cursor.Backward)
	if lo < n {
		res.Next = fwd
	}
	if start > 0 {
		res.Prev = back
	}
	return res, nil
}
