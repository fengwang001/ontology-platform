// Package agg 提供审计事件的并发安全存储与两维交叉计数。
package agg

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

// MaxTs 是事件时间戳与统计范围端点的最大合法取值（10^13）。
const MaxTs = int64(10_000_000_000_000)

var (
	// ErrInvalidParam 表示构造、追加或统计参数非法。
	ErrInvalidParam = errors.New("agg: 参数非法")
	// ErrCapacity 表示追加后事件总数将超过 Emax。
	ErrCapacity = errors.New("agg: 容量超限")
	// ErrTooLarge 表示交叉表 |行|×|列| 超过 Cmax。
	ErrTooLarge = errors.New("agg: 交叉表规模超限")
)

// Event 是一条审计事件：时间戳 Ts 与完整的维度取值集合 Dims。
type Event struct {
	Ts   int64
	Dims map[string]string
}

// Result 是 Query 的输出：字节序升序的行/列集合与真实计数矩阵。
type Result struct {
	Rows   []string
	Cols   []string
	Counts [][]int
}

// Store 是并发安全的事件存储。Append 串行化生效，Query 只读快照。
type Store struct {
	mu       sync.RWMutex
	dims     map[string]int
	roles    map[string]int
	kByLevel [3]int
	cmax     int
	emax     int
	events   []Event // 始终按 Ts 升序
	examined atomic.Int64
}

// New 校验构造参数并创建 Store。
func New(dims map[string]int, roles map[string]int, kByLevel [3]int, cmax, emax int) (*Store, error) {
	if len(dims) < 2 || len(dims) > 8 || cmax < 1 || emax < 1 {
		return nil, ErrInvalidParam
	}
	for _, lv := range dims {
		if lv < 1 || lv > 3 {
			return nil, ErrInvalidParam
		}
	}
	for _, lv := range roles {
		if lv < 1 || lv > 3 {
			return nil, ErrInvalidParam
		}
	}
	for i, k := range kByLevel {
		if k < 1 || (i > 0 && kByLevel[i-1] > k) {
			return nil, ErrInvalidParam
		}
	}
	s := &Store{
		dims:     make(map[string]int, len(dims)),
		roles:    make(map[string]int, len(roles)),
		kByLevel: kByLevel,
		cmax:     cmax,
		emax:     emax,
	}
	for name, lv := range dims {
		s.dims[name] = lv
	}
	for name, lv := range roles {
		s.roles[name] = lv
	}
	return s, nil
}

// DimLevel 返回维度敏感级别；未声明时 ok 为 false。
func (s *Store) DimLevel(name string) (lv int, ok bool) {
	lv, ok = s.dims[name]
	return lv, ok
}

// RoleLevel 返回角色可见级别；未知角色 ok 为 false。
func (s *Store) RoleLevel(name string) (lv int, ok bool) {
	lv, ok = s.roles[name]
	return lv, ok
}

// KFor 返回敏感级别 lv（1..3）对应的 k 值。
func (s *Store) KFor(lv int) int { return s.kByLevel[lv-1] }

// Append 校验并原子追加一批事件；任一事件非法则整批拒绝，
// 追加后总数超过 Emax 为容量错误；被拒绝时不改任何状态。
func (s *Store) Append(batch []Event) error {
	if len(batch) < 1 || len(batch) > 1000 {
		return ErrInvalidParam
	}
	for _, e := range batch {
		if err := s.checkEvent(e); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events)+len(batch) > s.emax {
		return ErrCapacity
	}
	for _, e := range batch {
		cp := Event{Ts: e.Ts, Dims: make(map[string]string, len(e.Dims))}
		for k, v := range e.Dims {
			cp.Dims[k] = v
		}
		s.events = append(s.events, cp)
	}
	sort.Slice(s.events, func(i, j int) bool { return s.events[i].Ts < s.events[j].Ts })
	return nil
}

func (s *Store) checkEvent(e Event) error {
	if e.Ts < 0 || e.Ts > MaxTs || len(e.Dims) != len(s.dims) {
		return ErrInvalidParam
	}
	for name := range s.dims {
		v, ok := e.Dims[name]
		if !ok || len(v) == 0 || len(v) > 64 {
			return ErrInvalidParam
		}
	}
	return nil
}

// Query 统计 [from,to) 内事件在 rowDim×colDim 上的交叉计数。
// 只扫描范围内事件；行/列集合确定后先判定规模再计数。
// 调用方须保证 rowDim、colDim 已声明且 from<to 合法。
func (s *Store) Query(rowDim, colDim string, from, to int64) (*Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lo := sort.Search(len(s.events), func(i int) bool { return s.events[i].Ts >= from })
	hi := sort.Search(len(s.events), func(i int) bool { return s.events[i].Ts >= to })
	type pair struct{ r, c string }
	pairs := make([]pair, 0, hi-lo)
	rowSet := map[string]struct{}{}
	colSet := map[string]struct{}{}
	for i := lo; i < hi; i++ {
		rv, cv := s.events[i].Dims[rowDim], s.events[i].Dims[colDim]
		rowSet[rv] = struct{}{}
		colSet[cv] = struct{}{}
		pairs = append(pairs, pair{rv, cv})
	}
	s.examined.Add(int64(hi - lo))
	if len(rowSet)*len(colSet) > s.cmax {
		return nil, ErrTooLarge
	}
	res := &Result{Rows: sortedKeys(rowSet), Cols: sortedKeys(colSet)}
	rowIdx := indexOf(res.Rows)
	colIdx := indexOf(res.Cols)
	res.Counts = make([][]int, len(res.Rows))
	for i := range res.Counts {
		res.Counts[i] = make([]int, len(res.Cols))
	}
	for _, p := range pairs {
		res.Counts[rowIdx[p.r]][colIdx[p.c]]++
	}
	return res, nil
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexOf(keys []string) map[string]int {
	m := make(map[string]int, len(keys))
	for i, k := range keys {
		m[k] = i
	}
	return m
}
