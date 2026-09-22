package node_test

import (
	"testing"

	"ontology/ival"
	"ontology/node"
)

func i(l, r int64) ival.Interval { return ival.Interval{L: l, R: r} }

func TestNewLeaf(t *testing.T) {
	n := node.New(i(2, 4))
	if n.Height != 1 || n.MaxR != 4 {
		t.Fatalf("leaf: height=%d maxR=%d", n.Height, n.MaxR)
	}
	if node.Count(n) != 1 {
		t.Fatalf("count=%d", node.Count(n))
	}
}

// buildSkewed 手动造一棵右斜树，再左旋，验证高度与 MaxR 修正。
func TestRotateLeftRefresh(t *testing.T) {
	//   1            3
	//    \          / \
	//     3   ->   1   5
	//      \
	//       5
	root := node.New(i(1, 2))
	mid := node.New(i(3, 8))
	leaf := node.New(i(5, 6))
	root.Right = mid
	mid.Right = leaf
	got := node.RotateLeft(root)
	if got.Iv.L != 3 || got.Left.Iv.L != 1 || got.Right.Iv.L != 5 {
		t.Fatalf("unexpected shape after rotate: %v", got.Iv)
	}
	if got.MaxR != 8 {
		t.Fatalf("root MaxR=%d want 8", got.MaxR)
	}
	if got.Left.MaxR != 2 || got.Right.MaxR != 6 {
		t.Fatalf("child MaxR wrong: %d %d", got.Left.MaxR, got.Right.MaxR)
	}
	if got.Height != 2 {
		t.Fatalf("height=%d want 2", got.Height)
	}
}

func TestRotateRightKeepsMaxR(t *testing.T) {
	//      5        3
	//     /          \
	//    3      ->    5
	root := node.New(i(5, 9))
	child := node.New(i(3, 4))
	root.Left = child
	got := node.RotateRight(root)
	if got.Iv.L != 3 || got.Right.Iv.L != 5 {
		t.Fatalf("unexpected shape")
	}
	if got.MaxR != 9 {
		t.Fatalf("MaxR=%d want 9", got.MaxR)
	}
}
