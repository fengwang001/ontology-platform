// Package rank 提供基于跨度的排名查询：At、RankOf、Range。
package rank

import (
	"errors"
	"sync/atomic"

	"ontology/key"
	"ontology/list"
)

var (
	ErrOutOfRange = errors.New("rank: 序号越界")
	ErrBadRange   = errors.New("rank: 区间下界大于上界")
)

// Rank 是排名查询器。visited 记录最近一次查询访问的节点数，
// 仅为非导出字段，用原子操作保证并发只读时无数据竞争。
type Rank struct {
	l       *list.List
	visited atomic.Int64
}

func New(l *list.List) *Rank { return &Rank{l: l} }

// bound 返回严格小于（strict）或小于等于 k 的元素个数。
func (r *Rank) bound(k key.Key, strict bool) int {
	x := r.l.Header()
	cnt := 0
	for i := r.l.MaxLevel() - 1; i >= 0; i-- {
		for x.Next[i] != nil {
			c := key.Compare(x.Next[i].Key, k)
			if c > 0 || (strict && c == 0) {
				break
			}
			cnt += x.Span[i]
			x = x.Next[i]
			r.visited.Add(1)
		}
	}
	return cnt
}

// At 返回序号为 k（从 0 计）的元素；越界返回 ErrOutOfRange。
func (r *Rank) At(k int) (key.Key, error) {
	if k < 0 || k >= r.l.Size() {
		return 0, ErrOutOfRange
	}
	r.visited.Store(0)
	x := r.l.Header()
	traversed := 0
	rank1 := k + 1 // 转为 1 基排名
	for i := r.l.MaxLevel() - 1; i >= 0; i-- {
		for x.Next[i] != nil && traversed+x.Span[i] <= rank1 {
			traversed += x.Span[i]
			x = x.Next[i]
			r.visited.Add(1)
		}
	}
	return x.Key, nil
}

// RankOf 返回 k 若存在时的第一个序号（即严格小于 k 的元素个数），
// found 表示 k 是否存在；对不存在的键返回其插入位置。
func (r *Rank) RankOf(k key.Key) (idx int, found bool) {
	r.visited.Store(0)
	x := r.l.Header()
	for i := r.l.MaxLevel() - 1; i >= 0; i-- {
		for x.Next[i] != nil && key.Compare(x.Next[i].Key, k) < 0 {
			idx += x.Span[i]
			x = x.Next[i]
			r.visited.Add(1)
		}
	}
	return idx, x.Next[0] != nil && key.Equal(x.Next[0].Key, k)
}

// Range 返回闭区间 [lo, hi] 内的元素个数；lo > hi 返回 ErrBadRange。
func (r *Rank) Range(lo, hi key.Key) (int, error) {
	if key.Compare(lo, hi) > 0 {
		return 0, ErrBadRange
	}
	r.visited.Store(0)
	return r.bound(hi, false) - r.bound(lo, true), nil
}

// LastVisited 返回最近一次查询访问的节点数（只读视图）。
func (r *Rank) LastVisited() int64 { return r.visited.Load() }
