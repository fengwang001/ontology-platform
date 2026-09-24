// Package mset 是对外多重集，串起 key/node/list/rank/iter 全部包。
package mset

import (
	"ontology/iter"
	"ontology/key"
	"ontology/list"
	"ontology/rank"
)

// Limits 是可配置的资源上限。
type Limits struct {
	MaxLevel int // 最大层数上限
	MaxElems int // 元素总数上限
}

// Mset 是确定性多重集。只读方法可并发；写方法需调用方串行化。
type Mset struct {
	l *list.List
	q *rank.Querier
}

// New 按上限创建空多重集；配置非法返回 list.ErrBadConfig。
func New(lim Limits) (*Mset, error) {
	l, err := list.New(lim.MaxLevel, lim.MaxElems)
	if err != nil {
		return nil, err
	}
	return &Mset{l: l, q: rank.New(l)}, nil
}

// Insert 插入一个副本；超限返回 list.ErrTooManyElements 或 list.ErrLevelLimit。
func (m *Mset) Insert(k key.K) error { return m.l.Insert(k) }

// Delete 删除一个副本；不存在返回 list.ErrNotFound。
func (m *Mset) Delete(k key.K) error { return m.l.Delete(k) }

// Count 返回等于 k 的元素个数。
func (m *Mset) Count(k key.K) int {
	lo, _ := m.l.Bound(k, false)
	hi, _ := m.l.Bound(k, true)
	return hi - lo
}

// At 返回第 i 个元素（0 起）；越界返回 list.ErrOutOfRange。
func (m *Mset) At(i int) (key.K, error) { return m.q.At(i) }

// RankOf 返回 k 的序号（第一个副本；不存在的键返回插入位置）。
func (m *Mset) RankOf(k key.K) int { return m.q.RankOf(k) }

// Range 返回闭区间 [lo,hi] 的元素个数；lo>hi 返回 rank.ErrBadRange。
func (m *Mset) Range(lo, hi key.K) (int, error) { return m.q.Range(lo, hi) }

// Iterate 返回从序号 from 开始的快照迭代器。
func (m *Mset) Iterate(from int) *iter.Iter { return iter.New(m.l, from) }

// Size 返回元素总数。
func (m *Mset) Size() int { return m.l.Size() }

// SelfCheck 核验跨度精确、每层有序、底层总数与规模一致。
func (m *Mset) SelfCheck() error { return m.l.SelfCheck() }

// Equal 判定两多重集结构逐字段相同。
func (m *Mset) Equal(other *Mset) bool { return list.Equal(m.l, other.l) }
