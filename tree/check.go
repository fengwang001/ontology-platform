package tree

import (
	"errors"
	"fmt"
	"math/bits"

	"ontology/ival"
	"ontology/node"
)

// ErrInvariant 是自检发现不变量被破坏时的根错误。
var ErrInvariant = errors.New("tree: invariant violation")

// InvariantError 携带具体的破坏位置说明。
type InvariantError struct {
	Detail string
}

func (e *InvariantError) Error() string   { return e.Detail }
func (e *InvariantError) Is(t error) bool { return t == ErrInvariant }

// SelfCheck 一次性核验：
//  1. 每个节点的 MaxR 等于其子树实际最大右端点；
//  2. BST 键序与 AVL 平衡（|平衡因子|<=1），树高在 O(log n) 上限内；
//  3. 实际节点总数与已插入计数一致。
func (t *Tree) SelfCheck() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	got := node.Count(t.root)
	if got != t.count {
		return &InvariantError{fmt.Sprintf("count: tracked=%d actual=%d", t.count, got)}
	}
	var prev *ival.Interval
	// 多重集约定：相等键一律进右子树。故左子树键必须严格小于本节点，
	// 右子树键允许等于本节点；中序为非递减序列。
	var check func(n *node.Node, lo, hi *ival.Interval) (height int, maxR int64, err error)
	check = func(n *node.Node, lo, hi *ival.Interval) (int, int64, error) {
		if n == nil {
			return 0, 0, nil
		}
		if lo != nil && cmp(n.Iv, *lo) < 0 {
			return 0, 0, &InvariantError{Detail: "BST order: key < lower bound"}
		}
		if hi != nil && cmp(n.Iv, *hi) > 0 {
			return 0, 0, &InvariantError{Detail: "BST order: key > upper bound"}
		}
		lh, lmax, errL := check(n.Left, lo, &n.Iv)
		if errL != nil {
			return 0, 0, errL
		}
		if prev != nil && cmp(*prev, n.Iv) > 0 {
			return 0, 0, &InvariantError{Detail: "inorder not sorted"}
		}
		iv := n.Iv
		prev = &iv
		rh, rmax, errR := check(n.Right, &n.Iv, hi)
		if errR != nil {
			return 0, 0, errR
		}
		if bf := lh - rh; bf > 1 || bf < -1 {
			return 0, 0, &InvariantError{Detail: fmt.Sprintf("balance factor %d at [%d,%d)", bf, n.Iv.L, n.Iv.R)}
		}
		height := 1 + lh
		if rh > height-1 {
			height = 1 + rh
		}
		if height != n.Height {
			return 0, 0, &InvariantError{Detail: fmt.Sprintf("stale height at [%d,%d)", n.Iv.L, n.Iv.R)}
		}
		maxR := n.Iv.R
		if n.Left != nil && lmax > maxR {
			maxR = lmax
		}
		if n.Right != nil && rmax > maxR {
			maxR = rmax
		}
		if maxR != n.MaxR {
			return 0, 0, &InvariantError{Detail: fmt.Sprintf("MaxR got %d want %d at [%d,%d)", n.MaxR, maxR, n.Iv.L, n.Iv.R)}
		}
		return height, maxR, nil
	}
	h, _, err := check(t.root, nil, nil)
	if err != nil {
		return err
	}
	bound := 3*bits.Len64(uint64(t.count)+1) + 2
	if h > bound {
		return &InvariantError{Detail: fmt.Sprintf("height %d exceeds bound %d (n=%d)", h, bound, t.count)}
	}
	return nil
}
