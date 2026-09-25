// Package id 定义并查集元素的下标类型，并转发 uf 的哨兵错误。
package id

import "ontology/uf"

// ID 是实例在并查集中的下标。
type ID int

// Set 在 uf.DSU 之上用强类型 ID 封装，避免裸 int 混用。
type Set struct{ d *uf.DSU }

// 哨兵错误直接转发 uf 包，errors.Is 可跨包区分。
var (
	ErrBadIndex     = uf.ErrBadIndex
	ErrNegativeSize = uf.ErrNegativeSize
	ErrEmptySet     = uf.ErrEmptySet
)

// New 创建 n 个独立 ID 的集合。
func New(n int) (Set, error) {
	d, err := uf.New(n)
	if err != nil {
		return Set{}, err
	}
	return Set{d}, nil
}

// Find 返回 x 的分量根 ID。
func (s Set) Find(x ID) (ID, error) {
	r, err := s.d.Find(int(x))
	return ID(r), err
}

// Union 合并两个 ID 所在分量。
func (s Set) Union(x, y ID) (bool, error) { return s.d.Union(int(x), int(y)) }

// Connected 判断两个 ID 是否等价（同分量）。
func (s Set) Connected(x, y ID) (bool, error) { return s.d.Connected(int(x), int(y)) }

// Count 返回分量数。
func (s Set) Count() int { return s.d.Count() }
