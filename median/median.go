// Package median 在 wmid 树上定位加权中位数：
// 沿树累加左子树权重和，找使前缀权重 P_k >= W/2 的最小 value。
package median

import (
	"errors"
	"sync/atomic"

	"ontology/wmid"
)

// ErrEmpty 空集合时查询加权中位数。
var ErrEmpty = errors.New("median: empty set")

// Finder 对一棵树执行加权中位数查询。
type Finder struct {
	tree *wmid.Tree
	// last 记录最近一次 Find 为定位结果而遍历的树节点个数。
	// 非导出，不出现在任何公开接口中。
	last atomic.Int64
}

// New 返回绑定到 t 的 Finder。
func New(t *wmid.Tree) *Finder { return &Finder{tree: t} }

// Find 返回当前加权中位数：按 value 升序的前缀权重 P_k 中，
// 首个满足 P_k >= W/2 的 value（W/2 按实数比较，
// 实现用等价整数式 2*P_k >= W，避免浮点误差）。
// 空集合返回 ErrEmpty，且不产生任何副作用。
func (f *Finder) Find() (int64, error) {
	root := f.tree.Root()
	if root == nil {
		return 0, ErrEmpty
	}
	total := root.Sum // W
	var visited int64
	var acc int64 // 严格小于当前节点的元素的权重和
	n := root
	for n != nil {
		visited++
		leftSum := int64(0)
		if l := n.Left(); l != nil {
			leftSum = l.Sum
		}
		prefix := acc + leftSum + n.Weight // 到本节点为止的 P_k
		switch {
		case 2*(acc+leftSum) >= total:
			// 左子树中已有前缀 >= W/2，答案在左侧。
			n = n.Left()
		case 2*prefix >= total:
			// 本节点是首个满足 P_k >= W/2 的节点。
			f.last.Store(visited)
			return n.Value, nil
		default:
			acc = prefix
			n = n.Right()
		}
	}
	// 不可达：total 为全树权重和，根处 prefix == total 必命中。
	f.last.Store(visited)
	return 0, ErrEmpty
}
