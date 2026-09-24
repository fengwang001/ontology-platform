// Package page 在可并发增删的有序数据集上做游标翻页（keyset pagination）。
//
// 数据集按复合键 (Score, ID) 全序排列，用有序切片 + 二分定位，
// 每次取页只检查"定位点附近一页"的行，绝不从头全扫。
package page

import (
	"sort"
	"sync"

	"ontology/cursor"
	"ontology/row"
)

// Store 是进程内存中的有序数据集，翻页与增删可并发进行。
type Store struct {
	mu   sync.RWMutex
	rows []row.Row
	ids  map[string]row.Row
}

// NewStore 用给定行构建数据集（拷贝并按复合键排序）。
func NewStore(seed []row.Row) *Store {
	s := &Store{ids: map[string]row.Row{}}
	for _, r := range seed {
		if row.Valid(r) {
			s.rows = append(s.rows, r)
			s.ids[r.ID] = r
		}
	}
	sort.Slice(s.rows, func(i, j int) bool { return row.Less(s.rows[i], s.rows[j]) })
	return s
}

// Snapshot 返回当前全部行的有序拷贝。
func (s *Store) Snapshot() []row.Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]row.Row, len(s.rows))
	copy(out, s.rows)
	return out
}

// Len 返回数据集当前行数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// Insert 并发安全地插入一行（重复 ID 不插入）。
func (s *Store) Insert(r row.Row) bool {
	if !row.Valid(r) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := sort.Search(len(s.rows), func(i int) bool { return row.Compare(s.rows[i], r) >= 0 })
	if idx < len(s.rows) && s.rows[idx].ID == r.ID {
		return false
	}
	s.rows = append(s.rows, row.Row{})
	copy(s.rows[idx+1:], s.rows[idx:])
	s.rows[idx] = r
	s.ids[r.ID] = r
	return true
}

// Delete 按 ID 删除一行，返回是否真的删除了。
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, ok := s.ids[id]
	if !ok {
		return false
	}
	idx := sort.Search(len(s.rows), func(i int) bool { return row.Compare(s.rows[i], target) >= 0 })
	if idx >= len(s.rows) || s.rows[idx].ID != id {
		return false
	}
	s.rows = append(s.rows[:idx], s.rows[idx+1:]...)
	delete(s.ids, id)
	return true
}

// Result 是一次取页的结果。Rows 始终按正向复合键序排列。
type Result struct {
	rows     []row.Row
	next     []byte
	prev     []byte
	compared int
	atStart  bool
}

func (r Result) Rows() []row.Row { return r.rows }
func (r Result) Next() []byte    { return r.next }
func (r Result) AtStart() bool   { return r.atStart }

// Prev 返回反向导航游标（backward 方向，指本页首行）。
func (r Result) Prev() []byte { return r.prev }

// Compared 返回本次取页实际做过的复合键比较次数。
func (r Result) Compared() int { return r.compared }

// Forward 正向取 limit 行。cur 为空表示从起点开始。
func (s *Store) Forward(cur []byte, limit int) (Result, error) {
	return s.fetch(cur, limit, cursor.Forward)
}

// Backward 反向取 limit 行（页内仍按正向序排列）。
func (s *Store) Backward(cur []byte, limit int) (Result, error) {
	return s.fetch(cur, limit, cursor.Backward)
}

func (s *Store) fetch(raw []byte, limit int, dir cursor.Direction) (Result, error) {
	dec, atStart, err := cursor.Decode(raw, dir)
	if err != nil {
		return Result{}, err
	}
	if limit <= 0 {
		limit = 1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	cnt := 0
	var picked []row.Row
	switch {
	case atStart && dir == cursor.Forward:
		picked = takeAsc(s, nil, limit, &cnt)
	case atStart:
		picked = takeDesc(s, nil, limit, &cnt)
	case dir == cursor.Forward:
		picked = takeAsc(s, &dec.Key, limit, &cnt)
	default:
		picked = takeDesc(s, &dec.Key, limit, &cnt)
	}
	res := Result{rows: picked, atStart: atStart}
	if len(picked) > 0 {
		res.next = cursor.Encode(picked[len(picked)-1], cursor.Forward)
		res.prev = cursor.Encode(picked[0], cursor.Backward)
	}
	res.compared = cnt
	return res, nil
}

// takeAsc 取严格大于 key 的前 limit 行（key=nil 表示从头）。
func takeAsc(s *Store, key *row.Key, limit int, cnt *int) []row.Row {
	lo := 0
	if key != nil {
		lo = sort.Search(len(s.rows), func(i int) bool {
			*cnt++
			return row.Compare(s.rows[i], *key) > 0
		})
	}
	hi := lo + limit
	if hi > len(s.rows) {
		hi = len(s.rows)
	}
	return append([]row.Row(nil), s.rows[lo:hi]...)
}

// takeDesc 取严格小于 key 的最后 limit 行，输出仍按正向序排列。
func takeDesc(s *Store, key *row.Key, limit int, cnt *int) []row.Row {
	n := len(s.rows)
	var hi int
	if key == nil {
		hi = n
	} else {
		hi = sort.Search(n, func(i int) bool {
			*cnt++
			return row.Compare(s.rows[i], *key) >= 0
		})
	}
	lo := hi - limit
	if lo < 0 {
		lo = 0
	}
	return append([]row.Row(nil), s.rows[lo:hi]...)
}
