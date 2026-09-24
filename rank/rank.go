// Package rank 提供排名查询：按序号取元素、按键求序号、区间计数。
package rank

import (
	"errors"
	"sync/atomic"

	"ontology/key"
	"ontology/list"
)

// ErrBadRange 表示 Range 的 lo > hi。
var ErrBadRange = errors.New("rank: lo > hi")

// Querier 包装跳表提供排名查询，并记录最近一次查询访问的节点数。
type Querier struct {
	l       *list.List
	visited atomic.Int64
}

func New(l *list.List) *Querier { return &Querier{l: l} }

// At 返回第 k 个元素（0 起）；越界返回 list.ErrOutOfRange。
func (q *Querier) At(k int) (key.K, error) {
	v, hops, err := q.l.At(k)
	q.visited.Store(int64(hops))
	return v, err
}

// RankOf 返回 k 的序号：存在的键返回第一个副本的序号，
// 不存在的键返回它若插入会占的位置（严格小于 k 的元素个数）。
func (q *Querier) RankOf(k key.K) int {
	pos, hops := q.l.Bound(k, false)
	q.visited.Store(int64(hops))
	return pos
}

// Range 返回闭区间 [lo,hi] 内的元素个数；lo>hi 返回 ErrBadRange。
func (q *Querier) Range(lo, hi key.K) (int, error) {
	if lo > hi {
		return 0, ErrBadRange
	}
	a, ha := q.l.Bound(lo, false)
	b, hb := q.l.Bound(hi, true)
	q.visited.Store(int64(ha + hb))
	return b - a, nil
}
