// Package tree 实现二叉 Merkle 树：分层构建、根哈希、认证路径生成与验证。
package tree

import (
	"encoding/binary"
	"errors"
	"sync/atomic"

	"ontology/fnv"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrEmptyLeaves     = errors.New("tree: empty leaf set")
	ErrNotPowerOfTwo   = errors.New("tree: leaf count is not a power of two")
	ErrIndexOutOfRange = errors.New("tree: index out of range")
	ErrRootMismatch    = errors.New("tree: reconstructed root does not match")
)

// Tree 构建后不可变；levels[0] 是叶子哈希，最后一层单元素即根。
type Tree struct {
	levels [][]uint32
	n      int
	last   atomic.Int64 // 最近一次 Verify 的 combine 次数；非导出，不出现在公开接口
}

// Build 校验叶子数（≥1 且为 2 的幂）后逐层合并；校验失败不产出任何状态。
func Build(leaves [][]byte) (*Tree, error) {
	n := len(leaves)
	if n == 0 {
		return nil, ErrEmptyLeaves
	}
	if n&(n-1) != 0 {
		return nil, ErrNotPowerOfTwo
	}
	lvl := make([]uint32, n)
	for i, d := range leaves {
		lvl[i] = fnv.Leaf(d)
	}
	levels := [][]uint32{lvl}
	for len(lvl) > 1 {
		next := make([]uint32, len(lvl)/2)
		for i := range next {
			next[i] = fnv.Combine(lvl[2*i], lvl[2*i+1])
		}
		levels = append(levels, next)
		lvl = next
	}
	return &Tree{levels: levels, n: n}, nil
}

// Root 返回根哈希（4 字节大端）。
func (t *Tree) Root() [4]byte {
	return toBytes(t.levels[len(t.levels)-1][0])
}

// LeafCount 返回叶子数。
func (t *Tree) LeafCount() int { return t.n }

// Proof 生成叶子 index 的认证路径：第 k 层兄弟下标为 (index>>k)^1。
func (t *Tree) Proof(index int) ([][4]byte, error) {
	if index < 0 || index >= t.n {
		return nil, ErrIndexOutOfRange
	}
	path := make([][4]byte, 0, len(t.levels)-1)
	for k := 0; k < len(t.levels)-1; k++ {
		path = append(path, toBytes(t.levels[k][(index>>k)^1]))
	}
	return path, nil
}

// Verify 仅凭路径从 leafHash(data) 重算到根并比对；任何失败都不改变树。
func (t *Tree) Verify(index int, data []byte, path [][4]byte) (bool, error) {
	if index < 0 || index >= t.n {
		return false, ErrIndexOutOfRange
	}
	cur := fnv.Leaf(data)
	var cnt int64
	for k, p := range path {
		s := binary.BigEndian.Uint32(p[:])
		if (index>>k)&1 == 0 {
			cur = fnv.Combine(cur, s)
		} else {
			cur = fnv.Combine(s, cur)
		}
		cnt++
	}
	t.last.Store(cnt)
	if cur != t.levels[len(t.levels)-1][0] {
		return false, ErrRootMismatch
	}
	return true, nil
}

func toBytes(h uint32) [4]byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], h)
	return b
}
