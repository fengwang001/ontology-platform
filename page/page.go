// Package page 在可并发增删的有序数据集上提供游标翻页。
//
// 翻页是纯函数：游标不可变、无隐藏推进状态。每页比较的行数有上界
// 4*(n + ceil(log2 N))：一次二分定位加一次长度为 n 的顺序扫描。
package page

import (
	"slices"
	"sync"

	"ontology/cursor"
	"ontology/row"
)

// Store 是并发安全的有序行集，按复合键 (Key, ID) 升序。
type Store struct {
	mu   sync.RWMutex
	rows []row.Row
}

// Insert 按复合键顺序插入一行。
func (s *Store) Insert(r row.Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, _ := slices.BinarySearchFunc(s.rows, r, row.Compare)
	s.rows = slices.Insert(s.rows, i, r)
}

// Delete 按 ID 删除一行，返回是否删除成功。
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rows {
		if r.ID == id {
			s.rows = slices.Delete(s.rows, i, i+1)
			return true
		}
	}
	return false
}

// All 返回当前全部行的升序快照。
func (s *Store) All() []row.Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.rows)
}

// Len 返回当前行数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// Page 是一次翻页的结果。compares 为非导出计数器，
// 记录本次翻页比较过的行数。
type Page struct {
	Rows     []row.Row
	Next     []byte // 正向续翻游标，本页为空时为 nil
	Prev     []byte // 反向续翻游标，本页为空时为 nil
	compares int
}

// Compares 返回本次翻页比较过的行数。
func (p Page) Compares() int { return p.compares }

// Pager 在 Store 上执行翻页，自身无状态。
type Pager struct{ store *Store }

// New 返回绑定到 s 的分页器。
func New(s *Store) *Pager { return &Pager{store: s} }

// CompareBound 返回单次翻页比较次数的上界：4*(n + ceil(log2 size))。
func CompareBound(n, size int) int {
	log2 := 0
	for x := size - 1; x > 0; x >>= 1 {
		log2++
	}
	return 4 * (n + log2)
}

// Next 正向取一页：复合键严格大于游标边界的前 n 行，升序。
// 空游标表示从头开始。
func (p *Pager) Next(cur []byte, n int) (Page, error) {
	c, err := cursor.Decode(cur)
	if err != nil {
		return Page{}, err
	}
	if err := c.RequireDir(cursor.Forward); err != nil {
		return Page{}, err
	}
	p.store.mu.RLock()
	defer p.store.mu.RUnlock()
	rows := p.store.rows
	start, cmp := 0, 0
	if !c.Empty() {
		start = upperBound(rows, row.Row{Key: c.Key, ID: c.ID}, &cmp)
	}
	end := min(start+n, len(rows))
	cmp += end - start
	return makePage(rows[start:end], cmp), nil
}

// Prev 反向取一页：复合键严格小于游标边界的最后 n 行，页内仍升序。
// 空游标表示从尾开始。
func (p *Pager) Prev(cur []byte, n int) (Page, error) {
	c, err := cursor.Decode(cur)
	if err != nil {
		return Page{}, err
	}
	if err := c.RequireDir(cursor.Backward); err != nil {
		return Page{}, err
	}
	p.store.mu.RLock()
	defer p.store.mu.RUnlock()
	rows := p.store.rows
	end, cmp := len(rows), 0
	if !c.Empty() {
		end = lowerBound(rows, row.Row{Key: c.Key, ID: c.ID}, &cmp)
	}
	start := max(end-n, 0)
	cmp += end - start
	return makePage(rows[start:end], cmp), nil
}

func makePage(rows []row.Row, cmp int) Page {
	p := Page{Rows: slices.Clone(rows), compares: cmp}
	if len(rows) > 0 {
		p.Next = cursor.Encode(cursor.After(rows[len(rows)-1], cursor.Forward))
		p.Prev = cursor.Encode(cursor.After(rows[0], cursor.Backward))
	}
	return p
}

// upperBound 返回首个复合键严格大于 key 的下标，cnt 累计比较次数。
func upperBound(rows []row.Row, key row.Row, cnt *int) int {
	lo, hi := 0, len(rows)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		*cnt++
		if row.Less(key, rows[mid]) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// lowerBound 返回首个复合键大于等于 key 的下标，cnt 累计比较次数。
func lowerBound(rows []row.Row, key row.Row, cnt *int) int {
	lo, hi := 0, len(rows)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		*cnt++
		if row.Less(rows[mid], key) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
