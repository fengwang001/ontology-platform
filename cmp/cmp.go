// Package cmp 在 lsn.Set 的升序集合上提供压缩重编号的双向映射：
// 第 k 小的旧 LSN 重编号为 k（0 起）。本包只依赖 lsn，依赖方向单向。
package cmp

import (
	"errors"

	"ontology/lsn"
)

// 哨兵错误：查询类失败，彼此不同，也与 lsn 的写入类错误不同。
var (
	// ErrUnknownLSN：FindNew 查询了一个集合中不存在的旧 LSN。
	ErrUnknownLSN = errors.New("cmp: unknown LSN, cannot find its new index")
	// ErrNewIndexOutOfRange：FindOld 的新号不在 [0,n) 内。
	ErrNewIndexOutOfRange = errors.New("cmp: new index out of range [0,n)")
)

// Mapper 是基于同一个 lsn.Set 的双向重编号映射，集合变化后名次即时生效。
type Mapper struct {
	set *lsn.Set
}

// NewMap 在给定集合上构建映射。
func NewMap(s *lsn.Set) *Mapper { return &Mapper{set: s} }

// FindNew 返回旧 LSN 对应的新号（其在集合中的 0 起升序名次）。
// 旧 LSN 不存在时整体失败：Mapper 无自身可变状态，集合不被触碰。
func (m *Mapper) FindNew(old int64) (int64, error) {
	rank, ok := m.set.IndexOf(old)
	if !ok {
		return 0, ErrUnknownLSN
	}
	return int64(rank), nil
}

// FindOld 返回新号对应的旧 LSN。new 越界（<0 或 >=n）时整体失败。
func (m *Mapper) FindOld(newIdx int64) (int64, error) {
	if newIdx < 0 || newIdx >= int64(m.set.Len()) {
		return 0, ErrNewIndexOutOfRange
	}
	v, ok := m.set.AtOK(int(newIdx))
	if !ok {
		// 理论不可达：上面已按 Len 判界；保留以防内部不变量被破坏。
		return 0, ErrNewIndexOutOfRange
	}
	return v, nil
}
