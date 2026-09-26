// Package api 是 Merkle 哈希树的对外接口，依赖 tree 包。
package api

import (
	"errors"
	"fmt"

	"ontology/fnv"
	"ontology/tree"
)

// 对外哨兵错误，与 tree 包同源，四者互不相同，可用 errors.Is 判定。
var (
	ErrEmptyLeaves     = tree.ErrEmptyLeaves
	ErrNotPowerOfTwo   = tree.ErrNotPowerOfTwo
	ErrIndexOutOfRange = tree.ErrIndexOutOfRange
	ErrRootMismatch    = tree.ErrRootMismatch
)

// Tree 是一棵已构建的 Merkle 树，构建后不可变，方法可并发调用。
type Tree struct{ t *tree.Tree }

// Build 由叶子数据构建 Merkle 树；叶子数必须为 2 的幂且 ≥1。
func Build(leaves [][]byte) (*Tree, error) {
	t, err := tree.Build(leaves)
	if err != nil {
		return nil, err
	}
	return &Tree{t: t}, nil
}

// Root 返回根哈希。
func (t *Tree) Root() [4]byte { return t.t.Root() }

// LeafCount 返回叶子数。
func (t *Tree) LeafCount() int { return t.t.LeafCount() }

// Proof 返回叶子 index 的认证路径。
func (t *Tree) Proof(index int) ([][4]byte, error) { return t.t.Proof(index) }

// Verify 仅凭路径验证 data 是第 index 个叶子且能还原树根。
func (t *Tree) Verify(index int, data []byte, path [][4]byte) (bool, error) {
	return t.t.Verify(index, data, path)
}

// SelfCheck 对内置叶子（"1".."8"）核验四条不变量，全部通过返回 nil。
func (t *Tree) SelfCheck() error {
	leaves := make([][]byte, 8)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprint(i + 1))
	}
	tr, err := Build(leaves)
	if err != nil {
		return err
	}
	for i, d := range leaves { // 不变量 1：路径可验证
		p, err := tr.Proof(i)
		if err != nil {
			return err
		}
		ok, err := tr.Verify(i, d, p)
		if err != nil || !ok {
			return fmt.Errorf("selfcheck: proof of leaf %d rejected", i)
		}
	}
	lvl := make([]uint32, len(leaves)) // 不变量 2：与朴素参照一致
	for i, d := range leaves {
		lvl[i] = fnv.Leaf(d)
	}
	for len(lvl) > 1 {
		next := make([]uint32, len(lvl)/2)
		for i := range next {
			next[i] = fnv.Combine(lvl[2*i], lvl[2*i+1])
		}
		lvl = next
	}
	var naive [4]byte
	naive[0], naive[1], naive[2], naive[3] = byte(lvl[0]>>24), byte(lvl[0]>>16), byte(lvl[0]>>8), byte(lvl[0])
	if tr.Root() != naive {
		return errors.New("selfcheck: root differs from naive reference")
	}
	p, _ := tr.Proof(0) // 不变量 3：防篡改
	if ok, err := tr.Verify(0, []byte("1x"), p); err == nil || ok {
		return errors.New("selfcheck: tampered leaf accepted")
	}
	root := tr.Root() // 不变量 4：失败不留痕
	if _, err := Build(nil); !errors.Is(err, ErrEmptyLeaves) {
		return errors.New("selfcheck: empty build not rejected")
	}
	if _, err := tr.Proof(-1); !errors.Is(err, ErrIndexOutOfRange) {
		return errors.New("selfcheck: bad index not rejected")
	}
	if tr.Root() != root {
		return errors.New("selfcheck: state changed after rejection")
	}
	return nil
}
