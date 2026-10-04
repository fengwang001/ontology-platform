// Package fresh 记录各数据集按周期的落地时刻，提供截止/违约判定，
// 并用 DSU 跳过结构维护每个数据集的最小未结案期号。
package fresh

import (
	"errors"
	"fmt"

	"ontology/dag"
)

var (
	ErrAlready         = errors.New("fresh: period already landed")
	ErrOutOfOrder      = errors.New("fresh: period out of order")
	ErrTooEarly        = errors.New("fresh: landing before period start")
	ErrUpstreamMissing = errors.New("fresh: upstream parent period not landed")
)

// Store 是落地记录与结案状态。
type Store struct {
	g    *dag.Graph
	land map[string][]int64         // name -> 按期号的落地时刻（严格按序追加）
	skip map[string]map[int64]int64 // DSU：已结案期号 -> 下一个候选期号
}

// New 基于依赖图创建落地记录存储。
func New(g *dag.Graph) *Store {
	return &Store{
		g:    g,
		land: make(map[string][]int64),
		skip: make(map[string]map[int64]int64),
	}
}

// Next 返回 d 的最小未落地期号。
func (s *Store) Next(d string) int64 { return int64(len(s.land[d])) }

// Deadline 返回 d 第 k 期的截止时刻 k*T+off。
func (s *Store) Deadline(d string, k int64) int64 {
	ds, _ := s.g.Get(d)
	return k*s.g.T + ds.Off
}

// Landed 返回 d 第 k 期的落地时刻；未落地时 ok=false。
func (s *Store) Landed(d string, k int64) (lt int64, ok bool) {
	l := s.land[d]
	if k >= 0 && k < int64(len(l)) {
		return l[k], true
	}
	return 0, false
}

// Violation 报告 (d,k) 在时刻 t 是否违约：
// 已落地且落地时刻严格大于截止；或未落地且 t 严格大于截止。
func (s *Store) Violation(d string, k, t int64) bool {
	dl := s.Deadline(d, k)
	if lt, ok := s.Landed(d, k); ok {
		return lt > dl
	}
	return t > dl
}

// Land 校验并记录 d 第 k 期在 now 落地。
// 拒绝次序：ErrAlready/ErrOutOfOrder > ErrTooEarly > ErrUpstreamMissing。
// 参数、时钟与数据集存在性由调用方（blame）先行校验。
func (s *Store) Land(d string, k, now int64) error {
	next := s.Next(d)
	if k < next {
		return fmt.Errorf("%w: %s period %d already landed", ErrAlready, d, k)
	}
	if k > next {
		return fmt.Errorf("%w: %s expects period %d, got %d", ErrOutOfOrder, d, next, k)
	}
	if now < k*s.g.T {
		return fmt.Errorf("%w: %s period %d starts at %d, now=%d", ErrTooEarly, d, k, k*s.g.T, now)
	}
	ds, _ := s.g.Get(d)
	missing := ""
	for _, p := range ds.Parents {
		if _, ok := s.Landed(p, k); !ok && (missing == "" || p < missing) {
			missing = p
		}
	}
	if missing != "" {
		return fmt.Errorf("%w: %s", ErrUpstreamMissing, missing)
	}
	s.land[d] = append(s.land[d], now)
	if now <= s.Deadline(d, k) {
		s.close(d, k) // 准时落地即结案
	}
	return nil
}

// Close 标记 (d,k) 结案（告警后调用），之后扫描不再考察它。
func (s *Store) Close(d string, k int64) { s.close(d, k) }

// FirstOpen 返回 d 的最小未结案期号。
func (s *Store) FirstOpen(d string) int64 { return s.find(d, 0) }

// NextOpen 返回 d 在 k 之后的最小未结案期号（要求 k 已结案）。
func (s *Store) NextOpen(d string, k int64) int64 { return s.find(d, k+1) }

func (s *Store) close(d string, k int64) {
	m := s.skip[d]
	if m == nil {
		m = make(map[int64]int64)
		s.skip[d] = m
	}
	m[k] = s.find(d, k+1)
}

// find 返回 >= k 的最小未结案期号，带路径压缩（迭代实现，避免深递归）。
func (s *Store) find(d string, k int64) int64 {
	m := s.skip[d]
	if m == nil {
		return k
	}
	root := k
	for {
		nxt, ok := m[root]
		if !ok {
			break
		}
		root = nxt
	}
	for cur := k; cur != root; {
		nxt, ok := m[cur]
		if !ok {
			break
		}
		m[cur] = root
		cur = nxt
	}
	return root
}
