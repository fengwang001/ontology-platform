// Package page 在一个可并发增删的有序数据集上做不透明游标分页（keyset pagination）。
// 每次翻页基于取数瞬间的快照；游标是纯复合键位置，不持有推进状态。
package page

import (
	"errors"
	"sort"
	"sync"

	"ontology/cursor"
	"ontology/row"
)

// Store 是按复合键升序保存的并发安全数据集。
type Store struct {
	mu   sync.RWMutex
	rows []row.Row
}

// NewStore 用初始数据构造数据集（按复合键排序）。
func NewStore(initial []row.Row) *Store {
	rows := append([]row.Row(nil), initial...)
	row.Sort(rows)
	return &Store{rows: rows}
}

// Add 插入一行，保持有序。ID 冲突时不重复插入。
func (s *Store) Add(r row.Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := sort.Search(len(s.rows), func(i int) bool {
		return !row.Less(row.KeyOf(s.rows[i]), row.KeyOf(r))
	})
	if idx < len(s.rows) && row.Equal(row.KeyOf(s.rows[idx]), row.KeyOf(r)) {
		return
	}
	next := make([]row.Row, 0, len(s.rows)+1)
	next = append(next, s.rows[:idx]...)
	next = append(next, r)
	next = append(next, s.rows[idx:]...)
	s.rows = next
}

// Delete 按 ID 删除一行，行不存在时无操作。
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]row.Row, 0, len(s.rows))
	for _, r := range s.rows {
		if r.ID != id {
			next = append(next, r)
		}
	}
	s.rows = next
}

// snapshot 返回当前有序切片；增删总是构造新切片，故旧快照不会被改写。
func (s *Store) snapshot() []row.Row {
	s.mu.RLock()
	rows := s.rows
	s.mu.RUnlock()
	return rows
}

// Paginator 在 Store 上以固定页大小翻页。
type Paginator struct {
	store *Store
	limit int

	// cmps 记录最近一次翻页比较过的行数（非导出，供包内测试白盒断言）。
	cmps int
}

// NewPaginator 构造分页器，limit 必须为正。
func NewPaginator(s *Store, limit int) *Paginator { return &Paginator{store: s, limit: limit} }

// Comparisons 返回最近一次翻页比较过的行数。
func (p *Paginator) Comparisons() int { return p.cmps }

// Page 是一页结果。Next/Prev 分别为正向/反向续翻游标，nil 表示该方向无更多页。
type Page struct {
	Rows    []row.Row
	Next    []byte
	Prev    []byte
	HasMore bool
}

var errLimit = errors.New("page: limit must be positive")

// Forward 正向取页：返回复合键严格大于游标位置的最小 limit 行，页内升序。
// 空游标表示从头开始。
func (p *Paginator) Forward(raw []byte) (Page, error) {
	if p.limit <= 0 {
		return Page{}, errLimit
	}
	k, _, err := cursor.Decode(raw, cursor.Forward)
	if err != nil && !errors.Is(err, cursor.ErrEmpty) {
		return Page{}, err
	}
	empty := errors.Is(err, cursor.ErrEmpty)

	p.cmps = 0
	rows := p.store.snapshot()
	start := 0
	if !empty {
		start = sort.Search(len(rows), func(i int) bool {
			p.cmps++
			return row.Less(k, row.KeyOf(rows[i])) // keys[i] > k
		})
	}
	return p.forwardWindow(rows, start), nil
}

func (p *Paginator) forwardWindow(rows []row.Row, start int) Page {
	end := start + p.limit
	if end > len(rows) {
		end = len(rows)
	}
	window := append([]row.Row(nil), rows[start:end]...)
	for range window {
		p.cmps++
	}
	out := Page{Rows: window, HasMore: end < len(rows)}
	if len(window) > 0 {
		out.Prev = cursor.Encode(row.KeyOf(window[0]), cursor.Backward)
		if out.HasMore {
			out.Next = cursor.Encode(row.KeyOf(window[len(window)-1]), cursor.Forward)
		}
	}
	return out
}

// Backward 反向取页：返回复合键严格小于游标位置的最大 limit 行，页内仍按升序排列。
// 空游标表示从末尾开始。
func (p *Paginator) Backward(raw []byte) (Page, error) {
	if p.limit <= 0 {
		return Page{}, errLimit
	}
	k, _, err := cursor.Decode(raw, cursor.Backward)
	if err != nil && !errors.Is(err, cursor.ErrEmpty) {
		return Page{}, err
	}
	empty := errors.Is(err, cursor.ErrEmpty)

	p.cmps = 0
	rows := p.store.snapshot()
	boundary := len(rows) // 第一个 keys[i] >= k 的位置；K < k 即其左侧
	if !empty {
		boundary = sort.Search(len(rows), func(i int) bool {
			p.cmps++
			return !row.Less(row.KeyOf(rows[i]), k) // keys[i] >= k
		})
	}
	start := boundary - p.limit
	if start < 0 {
		start = 0
	}
	window := append([]row.Row(nil), rows[start:boundary]...)
	for range window {
		p.cmps++
	}
	out := Page{Rows: window, HasMore: start > 0}
	if len(window) > 0 {
		out.Next = cursor.Encode(row.KeyOf(window[len(window)-1]), cursor.Forward)
		if out.HasMore {
			out.Prev = cursor.Encode(row.KeyOf(window[0]), cursor.Backward)
		}
	}
	return out, nil
}
